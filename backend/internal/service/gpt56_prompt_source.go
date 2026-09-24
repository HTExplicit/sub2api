package service

import (
	"context"
	"errors"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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

// ValidateBusinessSystemPromptSourceCandidateShape protects storage adapters
// without re-running the plugin's source provenance policy.
func ValidateBusinessSystemPromptSourceCandidateShape(candidate BusinessSystemPromptSourceCandidate) error {
	valuesPresent := candidate.ManagedSource != "" || candidate.SourceRepository != "" || candidate.SourceCommit != "" ||
		candidate.SourceVersion != "" || candidate.SourceArtifact != "" || candidate.SourceArtifactSHA256 != "" || candidate.SourceLicenseSHA256 != ""
	if !valuesPresent {
		return nil
	}
	if candidate.ManagedSource == "" || candidate.SourceRepository == "" || !isLowerHex(candidate.SourceCommit, 40) ||
		candidate.SourceVersion == "" || candidate.SourceArtifact == "" || !isLowerHex(candidate.SourceArtifactSHA256, 64) ||
		!isLowerHex(candidate.SourceLicenseSHA256, 64) || strings.TrimSpace(candidate.Body) == "" {
		return ErrBusinessSystemPromptSourceInvalid
	}
	hash, byteLength, err := ValidateBusinessSystemPromptBody(candidate.Body)
	if err != nil || !strings.EqualFold(hash, candidate.SHA256) || byteLength != candidate.ByteLength {
		return ErrBusinessSystemPromptSourceInvalid
	}
	return nil
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}
