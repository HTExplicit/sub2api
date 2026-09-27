package service

import (
	"context"
	"fmt"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type CodexProfileSelection struct {
	Profile          string    `json:"profile"`
	ExpectedRevision time.Time `json:"expected_revision"`
}

func (s *OpenAIGatewayService) SelectCodexProfile(ctx context.Context, id int64, choice CodexProfileSelection) error {
	if choice.Profile != "captured_windows_cli" && choice.Profile != "keep_legacy" {
		return codexRoutingUnavailable("unknown profile %q (expected captured_windows_cli or keep_legacy)", choice.Profile)
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	switch {
	case err != nil:
		return codexRoutingUnavailable("read account %d: %v", id, err)
	case !isOpenAICodexTicketAccount(account):
		return codexRoutingUnavailable("account %d is not an OpenAI OAuth/setup-token Codex account", id)
	}
	if choice.Profile == "keep_legacy" {
		return nil
	}
	if choice.ExpectedRevision.IsZero() || !choice.ExpectedRevision.Equal(account.UpdatedAt) {
		return fmt.Errorf("%w: account revision is %s, the request expected %s", ErrPluginStateChanged, account.UpdatedAt.UTC().Format(time.RFC3339Nano), choice.ExpectedRevision.UTC().Format(time.RFC3339Nano))
	}
	seed, valid := codexFingerprintSeed(account.Extra)
	if !valid {
		return codexRoutingUnavailable("account %d has no valid Codex device seed", id)
	}
	result, err := invokeCodexIdentityPolicyForAccount(ctx, account, "codex.identity.derive", extensionv1.CodexIdentityQuery{Seed: seed, Preset: choice.Profile})
	if err != nil {
		return codexRoutingUnavailable("derive profile %s: %v", choice.Profile, err)
	}
	if !result.Valid || result.Profile.Version != 2 {
		return codexRoutingUnavailable("derived profile %s is not usable (valid=%t, version=%d)", choice.Profile, result.Valid, result.Profile.Version)
	}
	writer, ok := s.accountRepo.(interface {
		UpdateExtraIfRevision(context.Context, int64, time.Time, map[string]any) (bool, error)
	})
	if !ok {
		return codexRoutingUnavailable("account repository cannot update extra by revision")
	}
	applied, err := writer.UpdateExtraIfRevision(ctx, id, choice.ExpectedRevision, map[string]any{CodexClientIdentityExtraKey: codexClientIdentityExtraValue(codexClientIdentity(result.Profile), time.Now())})
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("%w: the account changed while the profile was applied", ErrPluginStateChanged)
	}
	return nil
}
