package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketProxyMaskAndValidation(t *testing.T) {
	for _, raw := range []string{"http://user:secret@proxy.example.com:8080", "socks5h://user:secret@proxy.example.com:1080", "https://user:secret@[::1]:443"} {
		require.NoError(t, ValidateOpenAICodexTicketHarvestProxyURL(raw))
		masked := MaskProxyURL(raw)
		require.NotContains(t, masked, "secret")
		require.True(t, IsMaskedProxyURL(masked))
	}
	require.True(t, IsMaskedProxyURL(""))
	require.False(t, IsMaskedProxyURL("http://user:secret***suffix@proxy.example.com:8080"))
	for _, raw := range []string{"user:secret@host:1234", "http://user:secret@", "ftp://user:secret@host:1234", "http://user:secret@host:99999", "http://host:1234/?password=secret", "http://host:1234/#secret", "http://user:secret%zz@host:1234"} {
		err := ValidateOpenAICodexTicketHarvestProxyURL(raw)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
		require.Empty(t, MaskProxyURL(raw))
	}
}
