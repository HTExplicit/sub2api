package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const credentialIdentityDomain = "sub2api/account-credential-identity/v1"

var (
	ErrCredentialIdentityInvalid            = errors.New("invalid account credential identity")
	ErrCredentialIdentityConflict           = errors.New("credential identity already belongs to another account")
	ErrCredentialIdentityNotFound           = errors.New("credential identity not found")
	ErrCredentialIdentityGenerationConflict = errors.New("credential identity generation changed")
)

// AccountCredentialIdentity is the durable, non-secret identity used by later
// explicit account work. Generation advances when a credential is rotated.
type AccountCredentialIdentity struct {
	ID                int64  `json:"id"`
	AccountID         int64  `json:"account_id"`
	ProviderProfile   string `json:"provider_profile"`
	AuthType          string `json:"auth_type"`
	NormalizedBaseURL string `json:"normalized_base_url"`
	Fingerprint       string `json:"fingerprint"`
	Generation        int64  `json:"generation"`
	Active            bool   `json:"active"`
}

type BindAccountCredentialIdentityParams struct {
	AccountID           int64
	ProviderProfile     string
	AuthType            string
	NormalizedBaseURL   string
	Fingerprint         string
	ExpectedGeneration  int64
	ExpectedFingerprint string
}

type BindAccountCredentialIdentityResult struct {
	Identity AccountCredentialIdentity
	Rotated  bool
	Created  bool
}

type AccountCredentialIdentityRepository interface {
	Bind(ctx context.Context, params BindAccountCredentialIdentityParams) (*BindAccountCredentialIdentityResult, error)
	FindByFingerprint(ctx context.Context, fingerprint string) (*AccountCredentialIdentity, error)
	GetActiveByAccountID(ctx context.Context, accountID int64) (*AccountCredentialIdentity, error)
}

type AccountCredentialIdentityTransactionalRepository interface {
	AccountCredentialIdentityRepository
	BindInTransaction(ctx context.Context, tx AccountCredentialIdentityTransaction, params BindAccountCredentialIdentityParams) (*BindAccountCredentialIdentityResult, error)
}

type AccountCredentialIdentityTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func ValidateAccountCredentialIdentityBinding(params BindAccountCredentialIdentityParams) error {
	if params.AccountID <= 0 || strings.TrimSpace(params.ProviderProfile) != ProviderProfileCindyLaxaV1 ||
		strings.TrimSpace(params.AuthType) != AccountTypeAPIKey {
		return ErrCredentialIdentityInvalid
	}
	normalized, err := NormalizeCredentialIdentityBaseURL(params.ProviderProfile, params.NormalizedBaseURL)
	if err != nil || normalized != params.NormalizedBaseURL {
		return ErrCredentialIdentityInvalid
	}
	decoded, err := hex.DecodeString(params.Fingerprint)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(params.Fingerprint) != params.Fingerprint {
		return ErrCredentialIdentityInvalid
	}
	return nil
}

// NormalizeCredentialIdentityBaseURL accepts only Cindy's exact HTTPS root.
// It rejects paths, query strings, userinfo and explicit ports rather than
// collapsing distinct upstreams into a shared identity.
func NormalizeCredentialIdentityBaseURL(providerProfile, raw string) (string, error) {
	providerProfile = strings.ToLower(strings.TrimSpace(providerProfile))
	if providerProfile != ProviderProfileCindyLaxaV1 {
		return "", fmt.Errorf("%w: unsupported provider profile", ErrCredentialIdentityInvalid)
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%w: malformed base URL", ErrCredentialIdentityInvalid)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), cindyAPIHost) || parsed.Port() != "" {
		return "", fmt.Errorf("%w: unexpected base URL authority", ErrCredentialIdentityInvalid)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", fmt.Errorf("%w: base URL must be the provider root", ErrCredentialIdentityInvalid)
	}
	return "https://api.laxarouter.ai", nil
}

// AccountCredentialFingerprint uses an explicit domain and length-prefixed
// fields. The API-key bytes affect the digest but are never returned or stored.
func AccountCredentialFingerprint(providerProfile, authType, normalizedBaseURL, apiKey string) (string, error) {
	parts := []string{
		strings.ToLower(strings.TrimSpace(providerProfile)),
		strings.ToLower(strings.TrimSpace(authType)),
		strings.TrimSpace(normalizedBaseURL),
		apiKey,
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", ErrCredentialIdentityInvalid
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(credentialIdentityDomain))
	for _, part := range parts {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
