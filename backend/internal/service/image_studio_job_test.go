//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func TestPlanImageStudioEnforcesFixedModelsAndNativeOneFanout(t *testing.T) {
	previous := imageToolsConfigOverride.Load()
	t.Cleanup(func() { imageToolsConfigOverride.Store(previous) })
	ConfigureImageTools(&extensionv1.ImageToolsConfig{StudioEnabled: true})
	tests := []struct {
		name         string
		input        ImageStudioCreateInput
		hasReference bool
		hasMask      bool
		wantCode     string
	}{
		{
			name: "gpt generation fans out four one-image items",
			input: ImageStudioCreateInput{
				APIKeyID: 4, Mode: ImageStudioModeGenerate, Model: "gpt-image-2",
				Prompt: "draw a launch vehicle", Count: 4, Size: "1024x1024", Quality: "low",
			},
		},
		{
			name: "gemini edit accepts reference and mask",
			input: ImageStudioCreateInput{
				APIKeyID: 4, Mode: ImageStudioModeEdit, Model: "gemini-3-pro-image",
				Prompt: "replace the sky", Count: 2, Size: "1024x1024", Quality: "low",
			},
			hasReference: true,
			hasMask:      true,
		},
		{
			name: "gpt edit is not inferred",
			input: ImageStudioCreateInput{
				APIKeyID: 4, Mode: ImageStudioModeEdit, Model: "gpt-image-2",
				Prompt: "edit", Count: 1,
			},
			hasReference: true,
			wantCode:     "unsupported_mode",
		},
		{
			name: "gemini edit needs reference",
			input: ImageStudioCreateInput{
				APIKeyID: 4, Mode: ImageStudioModeEdit, Model: "gemini-3-pro-image",
				Prompt: "edit", Count: 1,
			},
			wantCode: "reference_required",
		},
		{
			name: "mask is edit only",
			input: ImageStudioCreateInput{
				APIKeyID: 4, Mode: ImageStudioModeGenerate, Model: "gemini-3-pro-image",
				Prompt: "draw", Count: 1,
			},
			hasMask:  true,
			wantCode: "mask_not_allowed",
		},
		{
			name: "count above four is rejected",
			input: ImageStudioCreateInput{
				APIKeyID: 4, Mode: ImageStudioModeGenerate, Model: "gpt-image-2",
				Prompt: "draw", Count: 5,
			},
			wantCode: "invalid_count",
		},
		{
			name: "unknown model is rejected",
			input: ImageStudioCreateInput{
				APIKeyID: 4, Mode: ImageStudioModeGenerate, Model: "gpt-image-3",
				Prompt: "draw", Count: 1,
			},
			wantCode: "unsupported_model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := planImageStudio(context.Background(), tt.input, tt.hasReference, tt.hasMask)
			if tt.wantCode == "" {
				require.NoError(t, err)
				return
			}
			var studioErr *ImageStudioError
			require.ErrorAs(t, err, &studioErr)
			require.Equal(t, tt.wantCode, studioErr.Code)
		})
	}
}

func TestResolveImageStudioTerminalStatusPreservesPartialAndCanceledResults(t *testing.T) {
	tests := []struct {
		name            string
		cancelRequested bool
		counts          ImageStudioCounts
		want            ImageStudioJobStatus
	}{
		{"all succeeded", false, ImageStudioCounts{Processed: 4, Succeeded: 4}, ImageStudioJobSucceeded},
		{"partial", false, ImageStudioCounts{Processed: 4, Succeeded: 2, Failed: 2}, ImageStudioJobPartiallySucceeded},
		{"all failed", false, ImageStudioCounts{Processed: 4, Failed: 4}, ImageStudioJobFailed},
		{"canceled no result", true, ImageStudioCounts{Processed: 4, Canceled: 4}, ImageStudioJobCanceled},
		{"canceled with result", true, ImageStudioCounts{Processed: 4, Succeeded: 1, Canceled: 3}, ImageStudioJobCanceledWithResults},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ResolveImageStudioTerminalStatus(tt.cancelRequested, tt.counts))
		})
	}
}

// A gateway failure keeps its status and body for administrators (server log
// and Ops); the job item shows the generic user-facing message.
func TestImageStudioGatewayErrorShowsGenericUserMessage(t *testing.T) {
	err := fmt.Errorf("generate: %w", &ImageStudioGatewayError{StatusCode: 502, Body: `{"error":{"message":"upstream account 7 was banned"}}`})
	code, message := imageStudioSafeExecutionError(context.Background(), err)
	require.Equal(t, "generation_failed", code)
	require.Equal(t, "Image generation failed", message)
}
