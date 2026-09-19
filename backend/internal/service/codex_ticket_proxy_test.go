package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketProxyInput(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"password: p@ss:#\nPort: 2721\nProxy Server: proxy.example.com\nusername: u+s", "http://u+s:p%40ss%3A%23@proxy.example.com:2721"},
		{"密码：abc\n用户名：user\n端口：8080\n主机：proxy.example.com", "http://user:abc@proxy.example.com:8080"},
		{"proxy.example.com:8080:user:p:a", "http://user:p%3Aa@proxy.example.com:8080"},
		{"user:pass@proxy.example.com:8080", "http://user:pass@proxy.example.com:8080"},
		{"socks5h://u:p@proxy.example.com:1080", "socks5h://u:p@proxy.example.com:1080"},
		{"[::1]:8080", "http://[::1]:8080"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := NormalizeCodexTicketProxy(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, input := range []string{"host: x\nport: 8080\nport: 90", "host: x\nport: 8080\nusername: u", "username: x\npassword: y", "user:pass:host:1234", "http://x:70000", "ftp://x:21"} {
		_, err := NormalizeCodexTicketProxy(input)
		require.Error(t, err)
	}
}
