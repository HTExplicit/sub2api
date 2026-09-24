// Package proxytransport shares bounded, cancellable proxy transport primitives
// between the host and independent plugins, without provider-specific policy.
package proxytransport

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const SOCKSDialTimeout = 10 * time.Second
const SOCKSKeepAlive = 30 * time.Second

func Configure(transport *http.Transport, address *url.URL, forward proxy.Dialer) error {
	if transport == nil {
		return fmt.Errorf("proxy transport is nil")
	}
	if address == nil {
		return nil
	}
	switch strings.ToLower(address.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(address)
		return nil
	case "socks5", "socks5h":
		if forward == nil {
			forward = &net.Dialer{Timeout: SOCKSDialTimeout, KeepAlive: SOCKSKeepAlive}
		}
		dialer, err := proxy.FromURL(address, forward)
		if err != nil {
			return fmt.Errorf("create SOCKS dialer: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return fmt.Errorf("SOCKS dialer does not support cancellation")
		}
		transport.Proxy = nil
		transport.DialContext = contextDialer.DialContext
		return nil
	default:
		return fmt.Errorf("unsupported proxy scheme: %s", address.Scheme)
	}
}
