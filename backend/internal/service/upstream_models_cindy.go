package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const cindyModelMetadataRegistryURL = "https://model-access.cindy.app/api/model-access/models?schemaVersion=4"

// Cindy remains an ordinary OpenAI API-key account. This public reference only
// enriches IDs from the authenticated list; it is never an access catalog.
func usesCindyModelMetadataRegistry(account *Account) bool {
	if account == nil || !account.IsOpenAI() || account.Type != AccountTypeAPIKey {
		return false
	}
	u, err := url.Parse(upstreamModelRegistryBaseURL(account))
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "api.laxarouter.ai") &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(u.Path == "" || u.Path == "/" || u.Path == "/v1" || u.Path == "/v1/")
}

func cindyCapabilitySyncModelIDs(account *Account, ids []string) []string {
	if !usesCindyModelMetadataRegistry(account) {
		return ids
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "cindy/auto-review" && id != "cindy/web-search" {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

func (s *AccountTestService) fetchUpstreamRegistryMetadata(ctx context.Context, account *Account, ids []string) (map[string]UpstreamModelMetadata, string, error) {
	if !usesCindyModelMetadataRegistry(account) {
		metadata, err := s.fetchModelsDevMetadata(ctx, account, ids)
		return metadata, "models.dev", err
	}
	registry, err := s.fetchCindyModelMetadataRegistry(ctx, account)
	if err != nil {
		return nil, "cindy-model-access", err
	}
	metadata := make(map[string]UpstreamModelMetadata)
	for _, id := range ids {
		if entry, ok := registry[id]; ok {
			metadata[id] = entry
		}
	}
	return metadata, "cindy-model-access", nil
}

func (s *AccountTestService) fetchCindyModelMetadataRegistry(ctx context.Context, account *Account) (map[string]UpstreamModelMetadata, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, fmt.Errorf("cindy model metadata registry is not configured")
	}
	result := s.cindyMetadataRegistryFlight.DoChan("global", func() (any, error) {
		s.cindyMetadataRegistryMu.Lock()
		cached, observedAt := s.cindyMetadataRegistry, s.cindyMetadataRegistryAt
		s.cindyMetadataRegistryMu.Unlock()
		if len(cached) > 0 && time.Since(observedAt) < modelsDevRegistryTTL {
			return cached, nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cindyModelMetadataRegistryURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := s.doUpstreamModelsRequest(req, upstreamModelsProxyURL(account), account)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("cindy model metadata registry returned HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamModelsBodyLimit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > upstreamModelsBodyLimit {
			return nil, fmt.Errorf("cindy model metadata registry exceeds %d bytes", upstreamModelsBodyLimit)
		}
		metadata, err := parseCindyModelMetadataRegistry(body)
		if err != nil {
			return nil, err
		}
		s.cindyMetadataRegistryMu.Lock()
		s.cindyMetadataRegistry, s.cindyMetadataRegistryAt = metadata, time.Now()
		s.cindyMetadataRegistryMu.Unlock()
		return metadata, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-result:
		if value.Err != nil {
			return nil, value.Err
		}
		metadata, ok := value.Val.(map[string]UpstreamModelMetadata)
		if !ok {
			return nil, fmt.Errorf("invalid cindy model metadata cache result")
		}
		return metadata, nil
	}
}

type cindyModelMetadataContract struct {
	ContextWindow   *int64   `json:"contextWindow"`
	MaxOutputTokens *int64   `json:"maxOutputTokens"`
	Efforts         []string `json:"efforts"`
	DefaultEffort   string   `json:"defaultEffort"`
}

func parseCindyModelMetadataRegistry(body []byte) (map[string]UpstreamModelMetadata, error) {
	var payload struct {
		SchemaVersion int `json:"schemaVersion"`
		Models        []struct {
			cindyModelMetadataContract
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Mode        string `json:"mode"`
			Modalities  struct {
				Input []string `json:"input"`
			} `json:"modalities"`
			PerAgent map[string]cindyModelMetadataContract `json:"perAgent"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid Cindy model metadata JSON: %w", err)
	}
	if payload.SchemaVersion != 4 || len(payload.Models) == 0 {
		return nil, fmt.Errorf("invalid Cindy model metadata schema or empty catalog")
	}
	metadata := make(map[string]UpstreamModelMetadata)
	seen := make(map[string]bool)
	for _, model := range payload.Models {
		id := strings.TrimSpace(model.ID)
		if !validModelContextID(id) || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate Cindy model metadata ID")
		}
		seen[id] = true
		if model.Mode != "chat" {
			continue
		}
		contract := model.cindyModelMetadataContract
		if codex, ok := model.PerAgent["codex"]; ok {
			if codex.ContextWindow != nil {
				contract.ContextWindow = codex.ContextWindow
			}
			if codex.MaxOutputTokens != nil {
				contract.MaxOutputTokens = codex.MaxOutputTokens
			}
			if codex.Efforts != nil {
				contract.Efforts = codex.Efforts
			}
			if codex.DefaultEffort != "" {
				contract.DefaultEffort = codex.DefaultEffort
			}
		}
		if contract.ContextWindow == nil || *contract.ContextWindow <= 0 ||
			(contract.MaxOutputTokens != nil && *contract.MaxOutputTokens <= 0) {
			return nil, fmt.Errorf("invalid Cindy model metadata capacity")
		}
		levels := normalizeReasoningLevels(contract.Efforts)
		reasoning := len(levels) > 0
		if len(levels) != len(contract.Efforts) || (contract.DefaultEffort != "" && !slices.Contains(levels, contract.DefaultEffort)) ||
			len(normalizeCodexInputModalities(model.Modalities.Input)) == 0 {
			return nil, fmt.Errorf("invalid Cindy model metadata capabilities")
		}
		entry := UpstreamModelMetadata{ID: id, DisplayName: model.Name, Description: model.Description,
			Reasoning: &reasoning, DefaultReasoningLevel: contract.DefaultEffort, SupportedReasoningLevels: levels,
			InputModalities: append([]string(nil), model.Modalities.Input...), ContextWindow: *contract.ContextWindow,
			CapacitySource: ModelContextSourceRegistry}
		if contract.MaxOutputTokens != nil {
			entry.MaxOutputTokens = *contract.MaxOutputTokens
		}
		metadata[id] = entry
	}
	if len(metadata) == 0 {
		return nil, fmt.Errorf("cindy model metadata has no conversation models")
	}
	return metadata, nil
}
