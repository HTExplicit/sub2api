//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateImageStudioCreateInputEnforcesFixedModelsAndNativeOneFanout(t *testing.T) {
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
			err := ValidateImageStudioCreateInput(tt.input, tt.hasReference, tt.hasMask)
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
