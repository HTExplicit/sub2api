package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// buildOpenAIAccountAvailableModels is the boundary between a public discovery
// catalog and /admin/accounts/:id/models. Identity and existing metadata remain
// unchanged; the UI contract always has a usable display name and model kind.
// This creates independent values and never enriches the shared cached body.
func buildOpenAIAccountAvailableModels(body []byte) ([]openai.Model, error) {
	_, entries, err := modelCatalogEntries(body, "data")
	if err != nil {
		return nil, fmt.Errorf("decode OpenAI account models: %w", err)
	}
	models := make([]openai.Model, 0, len(entries))
	for _, entry := range entries {
		var model openai.Model
		if err := json.Unmarshal(entry, &model); err != nil {
			return nil, fmt.Errorf("decode OpenAI account model: %w", err)
		}
		if strings.TrimSpace(model.ID) == "" {
			return nil, fmt.Errorf("OpenAI account model has no valid id")
		}
		if strings.TrimSpace(model.DisplayName) == "" {
			model.DisplayName = model.ID
		}
		if strings.TrimSpace(model.Type) == "" {
			model.Type = "model"
		}
		models = append(models, model)
	}
	return models, nil
}
