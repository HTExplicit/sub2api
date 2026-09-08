package service

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type AccountCapabilityDiscoveredModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// AccountCapabilityDiscoveryResult contains a fresh observation of the
// configured upstream only. "empty" is a successful empty list, never an
// unsupported endpoint, stale local mapping, fallback catalog or registry.
type AccountCapabilityDiscoveryResult struct {
	Status         string                             `json:"status"`
	Classification string                             `json:"classification"`
	HTTPStatus     int                                `json:"http_status,omitempty"`
	ErrorCode      string                             `json:"error_code,omitempty"`
	Reason         string                             `json:"reason"`
	RequestCount   int                                `json:"request_count"`
	LatencyMS      int64                              `json:"latency_ms"`
	ObservedAt     time.Time                          `json:"observed_at"`
	AccountFailure bool                               `json:"account_failure"`
	Source         string                             `json:"source"`
	Models         []AccountCapabilityDiscoveredModel `json:"models"`
}

// Discover intentionally does not call SyncUpstreamModelCatalog or
// fetchUpstreamModelList. The former mutates account metadata and queries an
// external registry, while the latter collapses a legitimate empty response
// into an error. Existing sync behavior is unchanged by this observer.
func (s *AccountCapabilityProbeService) Discover(ctx context.Context, account *Account) (result AccountCapabilityDiscoveryResult) {
	started := time.Now()
	result = AccountCapabilityDiscoveryResult{Status: "failed", Source: "upstream", Models: []AccountCapabilityDiscoveredModel{}}
	defer func() {
		result.ObservedAt = time.Now().UTC()
		result.LatencyMS = time.Since(started).Milliseconds()
	}()
	applyFailure := func(failure AccountCapabilityProbeAttempt) {
		result.Status, result.Classification, result.Reason = failure.Status, failure.Classification, failure.Reason
		result.HTTPStatus, result.ErrorCode, result.AccountFailure = failure.HTTPStatus, failure.ErrorCode, failure.AccountFailure
	}
	if failure := s.validateAccount(account); failure != nil {
		applyFailure(*failure)
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		applyFailure(capabilityProbeFailure("canceled", "canceled"))
		return result
	}
	requestCtx, cancel := context.WithTimeout(ctx, accountCapabilityRequestTimeout)
	defer cancel()
	requestCtx = WithHTTPUpstreamRedirectsDisabled(requestCtx)
	snapshot := *account
	req, err := s.accountTests.buildUpstreamModelsRequest(requestCtx, &snapshot)
	if err != nil {
		failure := capabilityProbeFailure("failed", "configuration_error")
		var syncError *UpstreamModelSyncError
		if errors.As(err, &syncError) && syncError.Kind == UpstreamModelSyncErrorUnsupported {
			failure = capabilityProbeFailure("unsupported", "catalog_unsupported")
		}
		applyFailure(failure)
		return result
	}
	req.GetBody = nil
	result.RequestCount = 1
	response, err := s.accountTests.doUpstreamModelsRequest(req, upstreamModelsProxyURL(&snapshot), &snapshot)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		applyFailure(accountCapabilityTransportFailure(err, requestCtx, AccountCapabilityProbeAttempt{}))
		return result
	}
	if response == nil || response.Body == nil {
		applyFailure(capabilityProbeFailure("uncertain", "invalid_response"))
		return result
	}
	defer func() { _ = response.Body.Close() }()
	result.HTTPStatus = response.StatusCode
	limit := resolveModelsListReadLimit(s.accountTests.cfg)
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		applyFailure(accountCapabilityTransportFailure(err, requestCtx, AccountCapabilityProbeAttempt{HTTPStatus: response.StatusCode}))
		return result
	}
	if int64(len(body)) > limit {
		applyFailure(AccountCapabilityProbeAttempt{Status: "failed", Classification: "invalid_catalog", Reason: accountCapabilityReason("invalid_catalog"), HTTPStatus: response.StatusCode})
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		applyFailure(accountCapabilityHTTPFailure(AccountCapabilityProbeAttempt{}, response.StatusCode, body, true))
		return result
	}
	models, err := accountCapabilityParseCatalog(body, snapshot.IsGrok())
	if err != nil {
		applyFailure(AccountCapabilityProbeAttempt{Status: "failed", Classification: "invalid_catalog", Reason: accountCapabilityReason("invalid_catalog"), HTTPStatus: response.StatusCode})
		return result
	}
	result.Models = models
	result.Status, result.Classification = "discovered", "catalog_discovered"
	if len(models) == 0 {
		result.Status, result.Classification = "empty", "catalog_empty"
	}
	// Pagination is explicit evidence, not a claim that the entire account
	// catalog was seen. No URL from the upstream is followed automatically.
	if gjson.GetBytes(body, "has_more").Bool() || gjson.GetBytes(body, "next_page_token").String() != "" || gjson.GetBytes(body, "nextPageToken").String() != "" {
		result.Status, result.Classification = "partial", "catalog_partial"
	}
	result.Reason = accountCapabilityReason(result.Classification)
	return result
}

func accountCapabilityParseCatalog(body []byte, grok bool) ([]AccountCapabilityDiscoveredModel, error) {
	root := gjson.ParseBytes(body)
	if !gjson.ValidBytes(body) || root.Type == gjson.Null || (!root.IsObject() && !root.IsArray()) {
		return nil, errors.New("invalid catalog envelope")
	}
	if root.IsObject() && root.Get("error").Exists() && root.Get("error").Type != gjson.Null {
		return nil, errors.New("upstream error is not a catalog")
	}
	entries, err := extractUpstreamModelRawEntries(body)
	if err != nil {
		return nil, err
	}
	models := make([]AccountCapabilityDiscoveredModel, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, raw := range entries {
		entry := gjson.ParseBytes(raw)
		id := ""
		if entry.Type == gjson.String {
			id = entry.String()
		} else if entry.IsObject() {
			paths := []string{"id", "slug", "name"}
			if grok {
				paths = []string{"model", "modelId", "model_id", "id", "slug", "_meta.model", "_meta.modelId", "_meta.model_id", "_meta.id", "_meta.slug", "_meta.name", "name"}
			}
			for _, path := range paths {
				candidate := entry.Get(path)
				if candidate.Type == gjson.String && strings.TrimSpace(candidate.String()) != "" {
					id = candidate.String()
					break
				}
			}
		}
		// Keep spelling, namespace and suffix exact. In particular, a genuine
		// "models/" namespace is not stripped by the older display helper.
		if id == "" || strings.TrimSpace(id) != id || len(id) > 512 || strings.ContainsAny(id, "\r\n\x00") {
			return nil, errors.New("invalid catalog model identifier")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		displayName := entry.Get("display_name").String()
		if entry.Get("display_name").Type != gjson.String || strings.TrimSpace(displayName) == "" {
			displayName = entry.Get("name").String()
			if entry.Get("name").Type != gjson.String || strings.TrimSpace(displayName) == "" {
				displayName = id
			}
		}
		if len(displayName) > 512 || strings.ContainsAny(displayName, "\r\n\x00") {
			displayName = id
		}
		models = append(models, AccountCapabilityDiscoveredModel{ID: id, DisplayName: displayName})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
