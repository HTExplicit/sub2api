package handler

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type managedModelWSGroupSource interface {
	LatestManagedModelGroup(context.Context, int64) (*service.Group, error)
	ValidateManagedModelCompilation(context.Context, *service.Group, *service.ManagedModelRequest) error
}

type managedModelWSAccountSource interface {
	ValidateManagedModelAccountLatest(context.Context, *service.Account, string) error
}

type managedModelWSTurn struct {
	turn    int
	request service.ManagedModelRequest
	mapped  bool
}

type managedModelWSEffectiveModel struct {
	publicModel string
	submitted   string
}

// A connection keeps its account and transport, but never keeps an obsolete
// publication alive. Only accepted turn hooks change the billing snapshot;
// raw frames may still be rejected by the existing overlap/lifecycle guard.
type managedModelWSGuard struct {
	groupID   int64
	platform  string
	groups    managedModelWSGroupSource
	accounts  managedModelWSAccountSource
	current   atomic.Pointer[managedModelWSTurn]
	effective atomic.Pointer[managedModelWSEffectiveModel]
}

func newManagedModelWSGuard(group *service.Group, groups managedModelWSGroupSource, accounts managedModelWSAccountSource) *managedModelWSGuard {
	if group == nil || !group.ManagedModelRoutes.Enabled || group.Platform == service.PlatformCindy {
		return nil
	}
	return &managedModelWSGuard{groupID: group.ID, platform: group.Platform, groups: groups, accounts: accounts}
}

// An unmanaged public connection cannot acquire a publication retroactively:
// its account/transport were selected without the managed invariant. Observe
// the latest flag on every raw frame and require reconnection after publication.
// Until then, return the exact input slice without parsing or rewriting it.
func observeUnmanagedModelWSFrame(ctx context.Context, groups managedModelWSGroupSource, group *service.Group, payload []byte) ([]byte, error) {
	if group == nil || group.IsExclusive || group.Platform == service.PlatformCindy {
		return payload, nil
	}
	if groups == nil {
		return nil, service.ErrManagedModelRouteUnavailable
	}
	latest, err := groups.LatestManagedModelGroup(ctx, group.ID)
	if err != nil || latest == nil || latest.ID != group.ID || latest.ManagedModelRoutes.Enabled {
		return nil, service.ErrManagedModelRouteUnavailable
	}
	return payload, nil
}

func (g *managedModelWSGuard) latestGroup(ctx context.Context) (*service.Group, error) {
	if g == nil || g.groups == nil {
		return nil, service.ErrManagedModelRouteUnavailable
	}
	group, err := g.groups.LatestManagedModelGroup(ctx, g.groupID)
	if err != nil || group == nil || group.ID != g.groupID || !group.IsActive() ||
		!group.ManagedModelRoutes.Enabled || group.Platform != g.platform {
		return nil, service.ErrManagedModelRouteUnavailable
	}
	return group, nil
}

func (g *managedModelWSGuard) resolve(ctx context.Context, model string) (*service.Group, *service.ManagedModelRequest, error) {
	group, err := g.latestGroup(ctx)
	if err != nil {
		return nil, nil, err
	}
	request, err := service.ResolveManagedModelRoute(group, model, service.ManagedModelEndpointResponsesWebSocket)
	if err != nil || request == nil || !isResponsesWebSocketCompositePlatform(request.Route.TargetPlatform) {
		return nil, nil, service.ErrManagedModelRouteUnavailable
	}
	if err := g.groups.ValidateManagedModelCompilation(ctx, group, request); err != nil {
		return nil, nil, service.ErrManagedModelRouteUnavailable
	}
	return group, request, nil
}

func (g *managedModelWSGuard) validateAccount(ctx context.Context, account *service.Account, request *service.ManagedModelRequest) error {
	if g == nil || g.accounts == nil || request == nil || account == nil {
		return service.ErrManagedModelRouteUnavailable
	}
	ctx = service.WithManagedModelRequest(ctx, request)
	ctx = service.WithResolvedTargetPlatform(ctx, request.Route.TargetPlatform)
	if err := g.accounts.ValidateManagedModelAccountLatest(ctx, account, request.Route.Selector); err != nil {
		return service.ErrManagedModelRouteUnavailable
	}
	return nil
}

// strictManagedWSObject rejects ambiguous structural keys before any parser or
// compatibility rewrite can choose a different duplicate. Input/tool objects
// are deliberately not traversed: their fields are application data.
func strictManagedWSObject(payload []byte) (event, sessionKey string, session gjson.Result, err error) {
	if !gjson.ValidBytes(payload) || !gjson.ParseBytes(payload).IsObject() {
		return "", "", gjson.Result{}, service.ErrManagedModelRouteUnavailable
	}
	typeCount, modelCount, sessionCount := 0, 0, 0
	gjson.ParseBytes(payload).ForEach(func(key, value gjson.Result) bool {
		switch strings.ToLower(key.String()) {
		case "type":
			typeCount++
			if key.String() != "type" || value.Type != gjson.String {
				err = service.ErrManagedModelRouteUnavailable
			}
			event = strings.TrimSpace(value.String())
		case "model":
			modelCount++
			if value.Type != gjson.String || strings.TrimSpace(value.String()) == "" {
				err = service.ErrManagedModelRouteUnavailable
			}
		case "session":
			sessionCount++
			sessionKey, session = key.String(), value
		}
		return true
	})
	if typeCount > 1 || modelCount > 1 || sessionCount > 1 {
		err = service.ErrManagedModelRouteUnavailable
	}
	if event == "" {
		event = "response.create"
	}
	return event, sessionKey, session, err
}

func managedWSObjectHasModel(value gjson.Result) bool {
	found := false
	value.ForEach(func(key, _ gjson.Result) bool {
		found = found || strings.EqualFold(key.String(), "model")
		return !found
	})
	return found
}

// prepareFrame runs on raw client bytes, including session.update. The second
// result is always a public request identity, even when session.model must be
// written as a verified wire ID before that configuration frame is forwarded.
func (g *managedModelWSGuard) prepareFrame(ctx context.Context, turn int, payload []byte, fallbackModel string, account *service.Account) ([]byte, *service.ManagedModelRequest, *service.Group, error) {
	event, sessionKey, session, err := strictManagedWSObject(payload)
	if err != nil {
		return nil, nil, nil, err
	}
	if turn == 1 && event != "response.create" {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	if event == "response.cancel" {
		if managedWSObjectHasModel(gjson.ParseBytes(payload)) || managedWSObjectHasModel(session) {
			return nil, nil, nil, service.ErrManagedModelRouteUnavailable
		}
		return payload, nil, nil, nil
	}
	if event != "response.create" && event != "session.update" {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	if event == "session.update" {
		if managedWSObjectHasModel(gjson.ParseBytes(payload)) || sessionKey != "session" || !session.IsObject() {
			return nil, nil, nil, service.ErrManagedModelRouteUnavailable
		}
		if !managedWSObjectHasModel(session) {
			return payload, nil, nil, nil
		}
	} else if managedWSObjectHasModel(session) {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	group, err := g.latestGroup(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	body := payload
	if event == "session.update" {
		body, fallbackModel = []byte(session.Raw), ""
	} else if !managedWSObjectHasModel(gjson.ParseBytes(payload)) {
		if effective := g.effective.Load(); effective != nil && strings.EqualFold(strings.TrimSpace(fallbackModel), effective.publicModel) {
			fallbackModel = effective.submitted
		}
	}
	body, request, err := service.PrepareManagedModelRequest(group, service.ManagedModelEndpointResponsesWebSocket, body, fallbackModel)
	if err != nil || request == nil || !isResponsesWebSocketCompositePlatform(request.Route.TargetPlatform) {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	if err := g.groups.ValidateManagedModelCompilation(ctx, group, request); err != nil {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	if account != nil || event == "session.update" {
		if err := g.validateAccount(ctx, account, request); err != nil {
			return nil, nil, nil, err
		}
	}
	if event == "session.update" {
		body, err = sjson.SetBytes(body, "model", account.GetMappedModel(request.Route.Selector))
		if err == nil {
			body, err = sjson.SetRawBytes(payload, "session", body)
		}
	} else if strings.TrimSpace(gjson.GetBytes(body, "type").String()) == "" {
		body, err = sjson.SetBytes(body, "type", "response.create")
	}
	if err != nil {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	// Reentrant first-frame preparation sees the canonical name from the
	// handler. Do not erase an effort alias already materialized on that frame.
	submitted := request.SubmittedModel
	if previous := g.effective.Load(); previous != nil && previous.publicModel == request.Route.PublicModel &&
		submitted == request.Route.PublicModel && turn == 1 {
		submitted = previous.submitted
	}
	g.effective.Store(&managedModelWSEffectiveModel{publicModel: request.Route.PublicModel, submitted: submitted})
	return body, request, group, nil
}

func (g *managedModelWSGuard) mappedTurn(ctx context.Context, turn int, request *service.ManagedModelRequest, account *service.Account) error {
	if err := g.validateAccount(ctx, account, request); err != nil {
		return err
	}
	g.current.Store(&managedModelWSTurn{turn: turn, request: *request, mapped: true})
	return nil
}

// validatePayload sees public bytes in passthrough mode and already mapped
// bytes in native mode. Only this turn's immutable successful map snapshot can
// authorize the latter; client-submitted selectors/wire IDs are rejected by
// prepareFrame before they can become such a snapshot.
func (g *managedModelWSGuard) validatePayload(ctx context.Context, turn int, payload []byte, originalModel string, account *service.Account) ([]byte, *service.Group, *service.ManagedModelRequest, error) {
	group, request, err := g.resolve(ctx, originalModel)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := g.validateAccount(ctx, account, request); err != nil {
		return nil, nil, nil, err
	}
	event, _, session, err := strictManagedWSObject(payload)
	if err != nil || event != "response.create" || managedWSObjectHasModel(session) {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	expected := request.Route.PublicModel
	mapped := false
	if previous := g.current.Load(); previous != nil && previous.turn == turn && previous.mapped {
		if previous.request.Route.PublicModel != request.Route.PublicModel || previous.request.Route.Selector != request.Route.Selector ||
			previous.request.Route.TargetPlatform != request.Route.TargetPlatform {
			return nil, nil, nil, service.ErrManagedModelRouteUnavailable
		}
		expected, mapped = account.GetMappedModel(request.Route.Selector), true
	}
	model := gjson.GetBytes(payload, "model")
	if model.Type != gjson.String || model.String() != expected {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	publicBody, err := sjson.SetBytes(payload, "model", request.Route.PublicModel)
	if err != nil {
		return nil, nil, nil, service.ErrManagedModelRouteUnavailable
	}
	g.current.Store(&managedModelWSTurn{turn: turn, request: *request, mapped: mapped})
	return publicBody, group, request, nil
}

func (g *managedModelWSGuard) validateTurn(ctx context.Context, turn int, account *service.Account) error {
	previous := g.current.Load()
	if previous == nil || previous.turn != turn {
		return service.ErrManagedModelRouteUnavailable
	}
	_, request, err := g.resolve(ctx, previous.request.Route.PublicModel)
	if err != nil || request.Route.Selector != previous.request.Route.Selector || request.Route.TargetPlatform != previous.request.Route.TargetPlatform {
		return service.ErrManagedModelRouteUnavailable
	}
	return g.validateAccount(ctx, account, request)
}
