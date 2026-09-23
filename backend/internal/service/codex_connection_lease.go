package service

import (
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

var (
	ErrCodexConnectionLeaseExpired   = errors.New("codex connection lease expired")
	ErrCodexConnectionLeaseScope     = errors.New("codex connection lease scope mismatch")
	ErrCodexConnectionLeaseTransport = errors.New("codex connection lease transport failed")
)

// CodexConnectionLeaseUpstream binds a routing qualification to an actual
// connection when the proxy cannot establish a stable egress identity. An empty
// leaseID is reserved for a host-authorized qualification request. Reusing a
// lease never dials a replacement connection, including transparent retries.
type CodexConnectionLeaseUpstream interface {
	DoWithCodexConnectionLease(req *http.Request, proxyURL string, accountID int64, scope string, leaseID string, expiresAt time.Time, profile *tlsfingerprint.Profile) (*http.Response, string, error)
}

// CodexConnectionLeaseInspector only examines a connection already owned by
// this process. It must not dial, send a probe, or extend the lease deadline.
type CodexConnectionLeaseInspector interface {
	CheckCodexConnectionLease(accountID int64, scope, leaseID string, expiresAt time.Time) error
}
