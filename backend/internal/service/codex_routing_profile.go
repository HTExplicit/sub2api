package service

import (
	"context"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type CodexProfileSelection struct {
	Profile          string    `json:"profile"`
	ExpectedRevision time.Time `json:"expected_revision"`
}

func (s *OpenAIGatewayService) SelectCodexProfile(ctx context.Context, id int64, choice CodexProfileSelection) error {
	if choice.Profile != "captured_windows_cli" && choice.Profile != "keep_legacy" {
		return errCodexRoutingUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil || !isOpenAICodexTicketAccount(account) {
		return errCodexRoutingUnavailable
	}
	if choice.Profile == "keep_legacy" {
		return nil
	}
	if choice.ExpectedRevision.IsZero() || !choice.ExpectedRevision.Equal(account.UpdatedAt) {
		return ErrPluginStateChanged
	}
	seed, valid := codexFingerprintSeed(account.Extra)
	if !valid {
		return errCodexRoutingUnavailable
	}
	result, err := invokeCodexIdentityPolicyForAccount(ctx, account, "codex.identity.derive", extensionv1.CodexIdentityQuery{Seed: seed, Preset: choice.Profile})
	if err != nil || !result.Valid || result.Profile.Version != 2 {
		return errCodexRoutingUnavailable
	}
	writer, ok := s.accountRepo.(interface {
		UpdateExtraIfRevision(context.Context, int64, time.Time, map[string]any) (bool, error)
	})
	if !ok {
		return errCodexRoutingUnavailable
	}
	applied, err := writer.UpdateExtraIfRevision(ctx, id, choice.ExpectedRevision, map[string]any{CodexClientIdentityExtraKey: codexClientIdentityExtraValue(codexClientIdentity(result.Profile), time.Now())})
	if err != nil {
		return err
	}
	if !applied {
		return ErrPluginStateChanged
	}
	return nil
}
