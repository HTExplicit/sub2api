package service

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeCredentialIdentityBaseURLIsExact(t *testing.T) {
	value, err := NormalizeCredentialIdentityBaseURL(ProviderProfileCindyLaxaV1, "https://API.LAXAROUTER.ai/")
	require.NoError(t, err)
	require.Equal(t, "https://api.laxarouter.ai", value)

	for _, invalid := range []string{
		"http://api.laxarouter.ai", "https://api.laxarouter.ai/v1", "https://api.laxarouter.ai?x=1",
		"https://user@api.laxarouter.ai", "https://api.laxarouter.ai:443", "https://example.com",
	} {
		_, err = NormalizeCredentialIdentityBaseURL(ProviderProfileCindyLaxaV1, invalid)
		require.ErrorIs(t, err, ErrCredentialIdentityInvalid, invalid)
	}
}

func TestAccountCredentialFingerprintPreservesRawKeyBytesAndSeparatesDomain(t *testing.T) {
	first, err := AccountCredentialFingerprint(ProviderProfileCindyLaxaV1, AccountTypeAPIKey, "https://api.laxarouter.ai", " key-one ")
	require.NoError(t, err)
	second, err := AccountCredentialFingerprint(ProviderProfileCindyLaxaV1, AccountTypeAPIKey, "https://api.laxarouter.ai", " key-one ")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first, sha256.Size*2)

	withoutWhitespace, err := AccountCredentialFingerprint(ProviderProfileCindyLaxaV1, AccountTypeAPIKey, "https://api.laxarouter.ai", "key-one")
	require.NoError(t, err)
	require.NotEqual(t, first, withoutWhitespace)

	params := BindAccountCredentialIdentityParams{
		AccountID: 1, ProviderProfile: ProviderProfileCindyLaxaV1, AuthType: AccountTypeAPIKey,
		NormalizedBaseURL: "https://api.laxarouter.ai", Fingerprint: first,
	}
	require.NoError(t, ValidateAccountCredentialIdentityBinding(params))
	params.Fingerprint = strings.ToUpper(first)
	require.ErrorIs(t, ValidateAccountCredentialIdentityBinding(params), ErrCredentialIdentityInvalid)
}
