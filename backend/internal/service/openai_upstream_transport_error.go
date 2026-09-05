package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
)

const (
	OpenAITransientTransportFailureReason  GatewayFailureReason = "openai_transient_transport_failure"
	OpenAIPersistentTransportFailureReason GatewayFailureReason = "openai_persistent_transport_failure"
)

// openAITransportFailoverBody is the OpenAI-format error body attached to the
// failover error for a transport-level failure. Kept identical to the legacy
// inline 502 body so the client-visible payload is unchanged if failover is
// ultimately exhausted.
var openAITransportFailoverBody = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)

// upstreamTransportErrorClass describes how to react to a transport-level upstream
// failure — i.e. the HTTP round-trip never completed (proxy / DNS / TCP / TLS
// error, no HTTP status code received).
type upstreamTransportErrorClass struct {
	// Persistent marks failures where retrying the same proxy/account is
	// unlikely to help: expired or rejected proxy credentials, a dead proxy
	// endpoint, or DNS/routing failure. The request-level retry state uses this
	// classification and installs a Redis cooldown only after its bounded retry.
	Persistent bool
}

// persistentUpstreamTransportErrorMarkers are substrings (matched case-insensitively
// against the raw transport error) that indicate a durable proxy/network fault.
// Matched signals are intentionally specific failure *reasons*, not the operation
// (e.g. we match "connection refused", not "proxyconnect") so that a transient
// failure of the same operation (a proxy timeout) is NOT misclassified as durable.
var persistentUpstreamTransportErrorMarkers = []string{
	"authentication failed",         // SOCKS5 RFC1929 / proxy credentials rejected (expired account)
	"proxy authentication required", // HTTP proxy 407
	"connection refused",            // proxy/upstream endpoint down
	"no route to host",
	"network is unreachable",
	"no such host", // DNS resolution failure (bad/expired proxy hostname)
}

// classifyUpstreamTransportError decides whether a transport-level upstream error
// is durable or a transient blip. Both remain request-local until the bounded
// retry state installs a runtime breaker cooldown.
//
// Motivating incident: a SOCKS5 proxy whose subscription lapsed returned
// `username/password authentication failed`; the account was nonetheless
// rescheduled on every request, hard-failing users with 502s.
//
// Classification strategy (mirrors sanitizeStreamError in gateway_service.go):
//  1. Typed-error checks first (syscall constants, *net.DNSError) — portable and
//     unambiguous.
//  2. String-marker fallback for errors that have no typed form (e.g. the plain
//     string returned by golang.org/x/net/proxy for SOCKS5 credential rejection).
//     The network-layer string markers ("connection refused", "no route to host",
//     "network is unreachable", "no such host") are kept as a cross-platform safety
//     net even though the typed checks should cover them on modern Go+Linux.
func classifyUpstreamTransportError(err error) upstreamTransportErrorClass {
	if err == nil {
		return upstreamTransportErrorClass{}
	}

	// — Typed checks (preferred) ——————————————————————————————————————————————
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return upstreamTransportErrorClass{Persistent: true}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return upstreamTransportErrorClass{Persistent: true}
	}

	// — String-marker fallback ————————————————————————————————————————————————
	msg := strings.ToLower(err.Error())
	for _, marker := range persistentUpstreamTransportErrorMarkers {
		if strings.Contains(msg, marker) {
			return upstreamTransportErrorClass{Persistent: true}
		}
	}
	return upstreamTransportErrorClass{}
}

// handleOpenAIUpstreamTransportError handles a transport-level upstream failure
// (Do/DoWithTLS returned a non-HTTP error: proxy/DNS/TCP/TLS). It:
//  1. records the failure in Ops error logs (status 0, kind=request_error);
//  2. classifies persistent versus transient transport failures without
//     mutating durable account scheduling state;
//  3. returns an error that is *UpstreamFailoverError (so the handler retries
//     once on the same account, then installs a runtime cooldown and fails over
//     to a healthy account) for all non-canceled errors, or a plain error for
//     context.Canceled (client gone — no failover or cooldown).
//
// It deliberately does NOT write to the response: the handler owns the response
// (failover, or a protocol-correct error once failover is exhausted).
//
// passthrough tags the Ops error event for the OpenAI passthrough forward path.
func (s *OpenAIGatewayService) handleOpenAIUpstreamTransportError(ctx context.Context, c *gin.Context, account *Account, err error, passthrough bool) error {
	safeErr := sanitizeUpstreamErrorMessage(err.Error())
	setOpsUpstreamError(c, 0, safeErr, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: 0,
		Passthrough:        passthrough,
		Kind:               "request_error",
		Message:            safeErr,
	})

	// Client disconnected: do NOT fail over to another account and do NOT evict
	// this one — the upstream never had a chance to exhibit a fault.
	if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return err
	}

	// Transport attempt reached the network path; count as Ollama Cloud activity.
	if s != nil {
		scheduleOllamaCloudUsageActivity(s.deferredService, account)
	}
	var pluginErr *PluginTransportError
	if errors.As(err, &pluginErr) && pluginErr.RequestSent {
		return err
	}

	transportClass := classifyUpstreamTransportError(err)
	reason := OpenAITransientTransportFailureReason
	if transportClass.Persistent {
		reason = OpenAIPersistentTransportFailureReason
	}

	return &UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: openAITransportFailoverBody,
		Reason:       reason,
	}
}
