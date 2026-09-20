//go:build unit

package service

import (
	"testing"

	cindyregistry "github.com/HTExplicit/sub2api-plugins/cindyprovider/catalog"
	"github.com/stretchr/testify/require"
)

func TestCindyCodexPresentationRejectsIdentityCapacityAndInstructionChanges(t *testing.T) {
	registry := cindyregistry.Registry{}
	for _, change := range []string{"missing", "identity", "capacity", "instructions", "reasoning", "compact"} {
		t.Run(change, func(t *testing.T) {
			var capability CindyCapability
			for _, candidate := range registry.CindyCapabilities() {
				if candidate.PublicID == "gpt-5.6-luna" {
					capability = candidate
				}
			}
			require.NotNil(t, capability.CodexPresentation)
			_, err := newCindyCodexModel(capability, 1)
			require.NoError(t, err)
			switch change {
			case "missing":
				capability.CodexPresentation = nil
			case "identity":
				capability.CodexPresentation.Slug = "another-provider"
			case "capacity":
				value := 1
				capability.CodexPresentation.ContextWindow = &value
			case "instructions":
				capability.CodexPresentation.BaseInstructions = "replace native host instructions"
			case "reasoning":
				value := "unsupported"
				capability.CodexPresentation.DefaultReasoningLevel = &value
			case "compact":
				value := int64(capability.EffectiveCodexContextWindow()) + 1
				capability.CodexPresentation.AutoCompactTokenLimit = &value
			}
			_, err = newCindyCodexModel(capability, 1)
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
		})
	}
}
