package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

const codexScopeFixtureSeed = "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"

func codexScopeFixtureAccount(id int64, kind string, stored bool) *Account {
	account := &Account{ID: id, Platform: PlatformOpenAI, Type: kind,
		Credentials: map[string]any{"refresh_token": "fixture-refresh"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: codexScopeFixtureSeed}}
	if stored {
		query, _ := json.Marshal(extensionv1.CodexIdentityQuery{Seed: codexScopeFixtureSeed})
		result, _ := (promptPolicyFixture{}).InvokeOperation(context.Background(), "", "", extensionv1.Invocation{
			Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.derive", Payload: query,
		})
		var identity extensionv1.CodexIdentityResult
		_ = json.Unmarshal(result.Payload, &identity)
		account.Extra[CodexClientIdentityExtraKey] = codexClientIdentityExtraValue(codexClientIdentity(identity.Profile), time.Unix(0, 0))
	}
	return account
}

func codexScopeFixtureManager(t *testing.T, percent int, kind string, calls *[]extensionv1.Invocation) *PluginManager {
	t.Helper()
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		*calls = append(*calls, in)
		return (promptPolicyFixture{}).InvokeOperation(withCodexTransportFixture(context.Background(), true), "", "", in)
	})
	installation := manager.extensions.Load().installations[1]
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: PlatformOpenAI,
		AccountType: kind, Enabled: true, RolloutPercent: percent}}
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityRequest: {
		"codex.identity.available", "codex.identity.plan", "codex.identity.derive",
		"codex.identity.validate", "codex.identity.agent", "codex.identity.sandbox", "codex.transport.plan",
	}}
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	return manager
}

func TestCodexAccountPoliciesCarryActualScope(t *testing.T) {
	actions := map[string]func(*testing.T, *Account){
		"outbound-identity": func(t *testing.T, account *Account) {
			_, err := resolveCodexOutboundIdentityForAccountContext(context.Background(), account, "")
			require.NoError(t, err)
		},
		"wire-transport": func(t *testing.T, account *Account) {
			req, err := http.NewRequest(http.MethodPost, "https://fixture.invalid/backend-api/codex/responses", strings.NewReader(`{"input":"fixture"}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			_, err = prepareCodexTransport(req, account)
			require.NoError(t, err)
		},
		"persisted-profile": func(_ *testing.T, account *Account) { _, _ = account.CodexClientIdentity() },
		"derived-profile": func(_ *testing.T, account *Account) {
			delete(account.Extra, CodexClientIdentityExtraKey)
			_, _ = account.CodexClientIdentity()
		},
		"fingerprint-default": func(_ *testing.T, account *Account) { _ = account.GetCodexFingerprintMode() },
		"metadata-update": func(_ *testing.T, account *Account) {
			_ = prepareCodexFingerprintExtraForUpdate(account, map[string]any{"fixture": true})
		},
		"diagnostic-snapshot": func(_ *testing.T, account *Account) {
			_ = resolveCodexIdentitySnapshotContext(context.Background(), account, account, "")
		},
		"gateway-sandbox": func(t *testing.T, account *Account) {
			account.Extra[codexFingerprintModeExtraKey] = string(codexFingerprintDevice)
			gateway := &OpenAIGatewayService{cfg: &config.Config{}}
			require.NotNil(t, gateway.resolveStagedCodexFingerprintIDs(nil, account, http.Header{}))
		},
	}
	for name, action := range actions {
		for _, percent := range []int{0, 50, 100} {
			for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
				for _, id := range []int64{2, 3} {
					t.Run(name+"/"+kind+"/"+strconv.Itoa(percent)+"/"+strconv.FormatInt(id, 10), func(t *testing.T) {
						var calls []extensionv1.Invocation
						codexScopeFixtureManager(t, percent, kind, &calls)
						account := codexScopeFixtureAccount(id, kind, true)
						action(t, account)
						if int(stablePluginBucket(id)) >= percent {
							require.Empty(t, calls, "outside-rollout account data must not enter a shared policy")
							return
						}
						require.NotEmpty(t, calls)
						for _, call := range calls {
							require.Equal(t, id, call.AccountID, "operation %s must keep the host's account scope", call.Operation)
						}
					})
				}
			}
		}
	}
}

func TestCodexAccountPolicyTypeDoesNotBecomeDomainScope(t *testing.T) {
	var calls []extensionv1.Invocation
	codexScopeFixtureManager(t, 100, AccountTypeSetupToken, &calls)
	account := codexScopeFixtureAccount(7, AccountTypeOAuth, true)
	_, err := resolveCodexOutboundIdentityForAccountContext(context.Background(), account, "")
	require.NoError(t, err)
	_ = prepareCodexFingerprintExtraForUpdate(account, map[string]any{})
	_ = resolveCodexIdentitySnapshotContext(context.Background(), account, account, "")
	ids := resolveCodexFingerprintIDs(account, "fixture-session", codexFingerprintDevice)
	ids.alignSandboxWithUserAgent("codex_cli_rs/0.150.0 (Windows 10.0.19045; x86_64) unknown")
	require.Empty(t, calls, "a setup-token binding must not validate/derive/interpret an OAuth account profile")
}

func TestCodexSharedIdentityBeforeAccountCreationKeepsDomainSemantics(t *testing.T) {
	var calls []extensionv1.Invocation
	codexScopeFixtureManager(t, 0, AccountTypeSetupToken, &calls)
	identity := deriveCodexClientIdentity(codexScopeFixtureSeed)
	require.True(t, identity.valid())
	require.NotEmpty(t, identity.UserAgent("0.150.0"))
	prepared := prepareCodexFingerprintExtraForCreate(PlatformOpenAI, AccountTypeSetupToken, map[string]any{})
	require.NotNil(t, prepared[CodexClientIdentityExtraKey])
	require.NotEmpty(t, calls)
	for _, call := range calls {
		require.Zero(t, call.AccountID, "shared/pre-create policy has no persisted account to invent")
	}
}

type codexScopeBackfillRepo struct {
	AccountRepository
	accounts []Account
	written  []int64
}

func (r *codexScopeBackfillRepo) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return r.accounts, nil
}

func (r *codexScopeBackfillRepo) UpdateExtraIfRevision(_ context.Context, id int64, _ time.Time, _ map[string]any) (bool, error) {
	r.written = append(r.written, id)
	return true, nil
}

func TestCodexIdentityBackfillUsesEachAccountScope(t *testing.T) {
	for _, percent := range []int{0, 50, 100} {
		t.Run(strconv.Itoa(percent), func(t *testing.T) {
			var calls []extensionv1.Invocation
			codexScopeFixtureManager(t, percent, AccountTypeOAuth, &calls)
			repo := &codexScopeBackfillRepo{accounts: []Account{
				*codexScopeFixtureAccount(2, AccountTypeOAuth, false),
				*codexScopeFixtureAccount(3, AccountTypeOAuth, false),
				*codexScopeFixtureAccount(7, AccountTypeSetupToken, false),
			}}
			updated, failed, err := NewCodexClientIdentityBackfillService(repo).RunOnce(context.Background())
			require.NoError(t, err)
			require.Zero(t, failed)
			var expected []int64
			for _, id := range []int64{2, 3} {
				if int(stablePluginBucket(id)) < percent {
					expected = append(expected, id)
				}
			}
			require.Equal(t, expected, repo.written)
			require.Equal(t, len(expected), updated)
			for _, call := range calls {
				require.Contains(t, expected, call.AccountID, "backfill must not use an account-free domain invocation")
			}
		})
	}
}

func TestCodexIdentityBackfillUnavailablePolicyIsAccountScoped(t *testing.T) {
	for _, percent := range []int{0, 100} {
		t.Run(strconv.Itoa(percent), func(t *testing.T) {
			var calls []extensionv1.Invocation
			manager := codexScopeFixtureManager(t, percent, AccountTypeOAuth, &calls)
			manager.extensions.Load().runtimes = map[int64]*pluginRuntime{}
			repo := &codexScopeBackfillRepo{accounts: []Account{*codexScopeFixtureAccount(2, AccountTypeOAuth, false)}}
			updated, failed, err := NewCodexClientIdentityBackfillService(repo).RunOnce(context.Background())
			if percent == 0 {
				require.NoError(t, err, "an account outside rollout must not bind the unavailable domain runtime")
			} else {
				require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
			}
			require.Zero(t, updated)
			require.Zero(t, failed)
			require.Empty(t, repo.written)
			require.Empty(t, calls)
		})
	}
}

func TestCodexRefreshScopeAndUnavailableRuntime(t *testing.T) {
	for _, percent := range []int{0, 50, 100} {
		for _, id := range []int64{2, 3} {
			for _, runtimeAvailable := range []bool{true, false} {
				t.Run(strconv.Itoa(percent)+"/"+strconv.FormatInt(id, 10)+"/"+strconv.FormatBool(runtimeAvailable), func(t *testing.T) {
					var calls []extensionv1.Invocation
					manager := codexScopeFixtureManager(t, percent, AccountTypeOAuth, &calls)
					if !runtimeAvailable {
						manager.extensions.Load().runtimes = map[int64]*pluginRuntime{}
					}
					account := codexScopeFixtureAccount(id, AccountTypeOAuth, true)
					client := &identityRefreshingOAuthClientStub{}
					service := NewOpenAIOAuthService(nil, client)
					require.Nil(t, service.privacyClientFactory, "the synthetic refresh fixture must not enrich via network")
					_, err := service.RefreshAccountToken(context.Background(), account)
					inScope := int(stablePluginBucket(id)) < percent
					if inScope && !runtimeAvailable {
						require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
						require.Empty(t, client.userAgent)
						return
					}
					require.NoError(t, err)
					require.NotEmpty(t, client.userAgent)
					if !inScope {
						require.Empty(t, calls)
					}
					for _, call := range calls {
						require.Equal(t, id, call.AccountID)
					}
				})
			}
		}
	}
}
