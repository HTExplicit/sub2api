package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const cindyOpaqueBindingIDPrefix = "cindy_opaque_v1_"

var errCindyOpaqueBindingConflict = errors.New("Cindy opaque continuation bindings disagree")

func cindyOpaqueBindingIDsFromItem(itemType string, item gjson.Result) []string {
	if !item.IsObject() {
		return nil
	}
	ids := make([]string, 0, 2)
	if carrier := item.Get("encrypted_content"); hasNonNullCindyContinuationCarrier(carrier) {
		ids = append(ids, cindyOpaqueBindingID("encrypted_content", carrier))
	}
	switch itemType {
	case "reasoning", "compaction", "compaction_summary":
		if carrier := item.Get("signature"); hasNonNullCindyContinuationCarrier(carrier) {
			ids = append(ids, cindyOpaqueBindingID("signature", carrier))
		}
	}
	return normalizeCindyOpaqueBindingIDs(ids)
}

func cindyOpaqueBindingID(kind string, carrier gjson.Result) string {
	canonical := canonicalCindyOpaqueCarrier(carrier)
	if canonical == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sub2api:cindy:opaque:v1\x00" + kind + "\x00" + canonical))
	return cindyOpaqueBindingIDPrefix + hex.EncodeToString(sum[:])
}

func canonicalCindyOpaqueCarrier(carrier gjson.Result) string {
	if !hasNonNullCindyContinuationCarrier(carrier) {
		return ""
	}
	if carrier.Type == gjson.String {
		return carrier.String()
	}
	var value any
	if err := json.Unmarshal([]byte(carrier.Raw), &value); err != nil {
		return strings.TrimSpace(carrier.Raw)
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return strings.TrimSpace(carrier.Raw)
	}
	return string(normalized)
}

func normalizeCindyOpaqueBindingIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	sort.Strings(normalized)
	return normalized
}

func cindyOpaqueBindingIDsFromResponsePayload(payload []byte) []string {
	if !gjson.ValidBytes(payload) {
		return nil
	}
	ids := make([]string, 0, 2)
	collectItem := func(item gjson.Result) {
		if !item.IsObject() {
			return
		}
		itemType := strings.TrimSpace(item.Get("type").String())
		ids = append(ids, cindyOpaqueBindingIDsFromItem(itemType, item)...)
	}
	collectItems := func(items gjson.Result) {
		if items.IsArray() {
			for _, item := range items.Array() {
				collectItem(item)
			}
			return
		}
		collectItem(items)
	}
	collectItems(gjson.GetBytes(payload, "output"))
	collectItems(gjson.GetBytes(payload, "response.output"))
	collectItems(gjson.GetBytes(payload, "item"))
	collectItem(gjson.ParseBytes(payload))
	return normalizeCindyOpaqueBindingIDs(ids)
}

func cindyOpaqueBindingIDsFromRawItems(items []json.RawMessage) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if !gjson.ValidBytes(item) {
			continue
		}
		parsed := gjson.ParseBytes(item)
		ids = append(ids, cindyOpaqueBindingIDsFromItem(strings.TrimSpace(parsed.Get("type").String()), parsed)...)
	}
	return normalizeCindyOpaqueBindingIDs(ids)
}

func LookupCindyOpaqueContinuationBinding(ctx context.Context, store OpenAIWSStateStore, groupID int64, bindingIDs []string) OpenAIContinuationBindingLookup {
	bindingIDs = normalizeCindyOpaqueBindingIDs(bindingIDs)
	if len(bindingIDs) == 0 {
		return OpenAIContinuationBindingLookup{State: OpenAIContinuationBindingMiss}
	}
	resolvedAccountID := int64(0)
	for _, bindingID := range bindingIDs {
		lookup := LookupOpenAIContinuationBinding(ctx, store, groupID, bindingID)
		if lookup.State != OpenAIContinuationBindingHit {
			return lookup
		}
		if resolvedAccountID == 0 {
			resolvedAccountID = lookup.AccountID
			continue
		}
		if lookup.AccountID != resolvedAccountID {
			return OpenAIContinuationBindingLookup{State: OpenAIContinuationBindingStoreError, Err: errCindyOpaqueBindingConflict}
		}
	}
	return OpenAIContinuationBindingLookup{State: OpenAIContinuationBindingHit, AccountID: resolvedAccountID}
}

func (s *OpenAIGatewayService) LookupCindyOpaqueContinuationBinding(ctx context.Context, groupID int64, bindingIDs []string) OpenAIContinuationBindingLookup {
	if s == nil {
		return OpenAIContinuationBindingLookup{State: OpenAIContinuationBindingMiss}
	}
	return LookupCindyOpaqueContinuationBinding(ctx, s.getOpenAIWSStateStore(), groupID, bindingIDs)
}

func (s *OpenAIGatewayService) SelectAccountByCindyOpaqueContinuation(
	ctx context.Context,
	groupID *int64,
	bindingIDs []string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requireCompact bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{Layer: "opaque_binding"}
	if s == nil {
		return nil, decision, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	ctx = s.withOpenAIProfitControlGate(ctx, groupID)
	lookup := LookupCindyOpaqueContinuationBinding(ctx, s.getOpenAIWSStateStore(), derefGroupID(groupID), bindingIDs)
	switch lookup.State {
	case OpenAIContinuationBindingStoreError:
		return nil, decision, NewOpenAIContinuationStoreUnavailableError()
	case OpenAIContinuationBindingHit:
	default:
		return nil, decision, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	if excludedIDs != nil {
		if _, excluded := excludedIDs[lookup.AccountID]; excluded {
			return nil, decision, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
		}
	}

	account, err := s.getSchedulableAccount(ctx, lookup.AccountID)
	if err != nil || account == nil || !s.openAIAccountMatchesSchedulingGroup(account, groupID) {
		return nil, decision, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	account = s.recheckSelectedOpenAIAccountFromDB(
		ctx, account, groupID, PlatformCindy, requestedModel, requireCompact, requiredCapability,
	)
	if account == nil || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		!s.isOpenAIAccountTransportCompatible(account, requiredTransport) {
		return nil, decision, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}

	acquired, acquireErr := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if acquireErr != nil {
		return nil, decision, acquireErr
	}
	selection := &AccountSelectionResult{Account: account}
	if acquired != nil && acquired.Acquired {
		selection.Acquired = true
		selection.ReleaseFunc = acquired.ReleaseFunc
	} else if s.concurrencyService != nil {
		cfg := s.schedulingConfig()
		selection.WaitPlan = &AccountWaitPlan{
			AccountID: account.ID, MaxConcurrency: account.Concurrency,
			Timeout: cfg.StickySessionWaitTimeout, MaxWaiting: cfg.StickySessionMaxWaiting,
		}
	} else {
		return nil, decision, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	decision.SelectedAccountID = account.ID
	decision.SelectedAccountType = account.Type
	return attachSelectionProfitGate(ctx, attachSelectionRuntimeBreakerProbe(ctx, selection)), decision, nil
}

func bindCindyOpaqueContinuationAccount(ctx context.Context, store OpenAIWSStateStore, groupID, accountID int64, bindingIDs []string, ttl time.Duration) error {
	if store == nil || accountID <= 0 {
		return nil
	}
	for _, bindingID := range normalizeCindyOpaqueBindingIDs(bindingIDs) {
		if err := store.BindResponseAccount(ctx, groupID, bindingID, accountID, ttl); err != nil {
			return err
		}
	}
	return nil
}

func (s *OpenAIGatewayService) bindCindyOpaqueContinuationAccount(ctx context.Context, c *gin.Context, account *Account, bindingIDs []string) {
	if s == nil || account == nil || account.ID <= 0 || len(bindingIDs) == 0 ||
		!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	if err := bindCindyOpaqueContinuationAccount(
		ctx, s.getOpenAIWSStateStore(), groupID, account.ID, bindingIDs, s.openAIWSResponseStickyTTL(),
	); err != nil {
		logOpenAIWSModeInfo("bind_cindy_opaque_account_failed group_id=%d account_id=%d", groupID, account.ID)
	}
}
