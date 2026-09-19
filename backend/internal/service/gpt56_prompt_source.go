package service

import (
	"context"
	"errors"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var (
	ErrBusinessSystemPromptSourceUnavailable    = errors.New("business system prompt source unavailable")
	ErrBusinessSystemPromptSourceInvalid        = errors.New("business system prompt source invalid")
	ErrBusinessSystemPromptSourceLicenseChanged = errors.New("business system prompt source license changed")
)

type BusinessSystemPromptSourceCandidate = extensionv1.PromptSourceCandidate
type BusinessSystemPromptSource interface {
	Fetch(context.Context) (BusinessSystemPromptSourceCandidate, error)
}
type GitHubGPT56PromptSource struct{}

func NewGitHubGPT56PromptSource() *GitHubGPT56PromptSource { return &GitHubGPT56PromptSource{} }

func (*GitHubGPT56PromptSource) Fetch(ctx context.Context) (BusinessSystemPromptSourceCandidate, error) {
	var output extensionv1.PromptSourceEnvelope
	if err := invokePromptManagement(ctx, "prompt.source.fetch", struct{}{}, &output); err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	output.Candidate.Body = output.Body
	return output.Candidate, nil
}

func ValidateBusinessSystemPromptSourceCandidate(candidate BusinessSystemPromptSourceCandidate) error {
	return invokePromptManagement(context.Background(), "prompt.source.validate", extensionv1.PromptSourceEnvelope{Candidate: candidate, Body: candidate.Body}, nil)
}
