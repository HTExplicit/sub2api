package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	WirePlatformOpenAI         = PlatformOpenAI
	ProviderProfileCindyLaxaV1 = "cindy_laxa_v1"
)

// ResolveAccountProviderIdentity returns the persisted semantic platform,
// protocol handler family and provider isolation profile. Legacy OpenAI+Laxa
// rows retain ordinary OpenAI provider identity even while temporary
// account-level compatibility recognizes them; migration 229 owns their
// one-time projection to the first-class Cindy identity.
func ResolveAccountProviderIdentity(platform, accountType string, credentials map[string]any) (string, string, string, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return "", "", "", fmt.Errorf("platform is required")
	}
	if platform == PlatformCindy {
		if !IsCindyAPIKeyAccount(platform, accountType, credentials) {
			return "", "", "", fmt.Errorf("cindy accounts require an API key and exact https://api.laxarouter.ai base_url")
		}
		return PlatformCindy, WirePlatformOpenAI, ProviderProfileCindyLaxaV1, nil
	}
	return platform, platform, "", nil
}

func ResolveGroupProviderIdentity(platform string) (string, string, string, error) {
	platform = strings.ToLower(strings.TrimSpace(NormalizeGroupPlatform(platform)))
	if platform == PlatformCindy {
		return PlatformCindy, WirePlatformOpenAI, ProviderProfileCindyLaxaV1, nil
	}
	if platform == "" {
		return "", "", "", fmt.Errorf("platform is required")
	}
	return platform, platform, "", nil
}

func (a *Account) EffectiveWirePlatform() string {
	if a == nil {
		return ""
	}
	if wire := strings.ToLower(strings.TrimSpace(a.WirePlatform)); wire != "" {
		return wire
	}
	if a.Platform == PlatformCindy {
		return WirePlatformOpenAI
	}
	return strings.ToLower(strings.TrimSpace(a.Platform))
}

func (a *Account) EffectiveProviderProfile() string {
	if a == nil {
		return ""
	}
	if profile := strings.ToLower(strings.TrimSpace(a.ProviderProfile)); profile != "" {
		return profile
	}
	if a.Platform == PlatformCindy {
		return ProviderProfileCindyLaxaV1
	}
	return ""
}

func (g *Group) EffectiveWirePlatform() string {
	if g == nil {
		return ""
	}
	if wire := strings.ToLower(strings.TrimSpace(g.WirePlatform)); wire != "" {
		return wire
	}
	if g.Platform == PlatformCindy {
		return WirePlatformOpenAI
	}
	return strings.ToLower(strings.TrimSpace(g.Platform))
}

func (g *Group) EffectiveProviderProfile() string {
	if g == nil {
		return ""
	}
	if profile := strings.ToLower(strings.TrimSpace(g.ProviderProfile)); profile != "" {
		return profile
	}
	if g.Platform == PlatformCindy {
		return ProviderProfileCindyLaxaV1
	}
	return ""
}

// ProviderIdentityCompatible is the common scheduler and binding isolation
// gate. A shared wire handler never permits cross-platform/profile fallback.
func ProviderIdentityCompatible(account *Account, group *Group) bool {
	if account == nil || group == nil {
		return false
	}
	return account.Platform == group.Platform &&
		account.EffectiveWirePlatform() == group.EffectiveWirePlatform() &&
		account.EffectiveProviderProfile() == group.EffectiveProviderProfile()
}

func hasCanonicalCindyProviderIdentity(account *Account) bool {
	return account != nil &&
		account.Platform == PlatformCindy &&
		account.EffectiveWirePlatform() == WirePlatformOpenAI &&
		account.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1 &&
		IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
}

func providerGroupIdentityCompatible(left, right *Group) bool {
	if left == nil || right == nil {
		return false
	}
	return left.Platform == right.Platform &&
		left.EffectiveWirePlatform() == right.EffectiveWirePlatform() &&
		left.EffectiveProviderProfile() == right.EffectiveProviderProfile()
}

func validateProviderIdentityAccountsForGroup(ctx context.Context, repo AccountRepository, group *Group, accountIDs []int64) error {
	if group == nil || len(accountIDs) == 0 {
		return nil
	}
	if repo == nil {
		return errors.New("account repository not configured")
	}
	accounts, err := repo.GetByIDs(ctx, accountIDs)
	if err != nil {
		return fmt.Errorf("get accounts: %w", err)
	}
	if len(accounts) != len(accountIDs) {
		return fmt.Errorf("one or more accounts do not exist")
	}
	for _, account := range accounts {
		if group.Platform == PlatformComposite {
			if account != nil && account.Platform != PlatformCindy && account.EffectiveProviderProfile() != ProviderProfileCindyLaxaV1 {
				continue
			}
			return fmt.Errorf("cindy accounts cannot bind to composite group %d", group.ID)
		}
		if account == nil || !ProviderIdentityCompatible(account, group) {
			return fmt.Errorf("account provider identity does not match group %d identity %s/%s/%s",
				group.ID, group.Platform, group.EffectiveWirePlatform(), group.EffectiveProviderProfile())
		}
	}
	return nil
}

func validateProviderIdentityGroupBindings(ctx context.Context, repo GroupRepository, account *Account, groupIDs []int64) error {
	if account == nil || len(groupIDs) == 0 {
		return nil
	}
	if repo == nil {
		return errors.New("group repository not configured")
	}
	for _, groupID := range groupIDs {
		group, err := repo.GetByID(ctx, groupID)
		if err != nil {
			return fmt.Errorf("get group: %w", err)
		}
		if group.Platform == PlatformComposite {
			if account.Platform == PlatformCindy || account.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1 {
				return fmt.Errorf("cindy accounts cannot bind to composite group %d", groupID)
			}
			continue
		}
		if !ProviderIdentityCompatible(account, group) {
			return fmt.Errorf(
				"account provider identity %s/%s/%s does not match group %d identity %s/%s/%s",
				account.Platform, account.EffectiveWirePlatform(), account.EffectiveProviderProfile(),
				groupID, group.Platform, group.EffectiveWirePlatform(), group.EffectiveProviderProfile(),
			)
		}
	}
	return nil
}
