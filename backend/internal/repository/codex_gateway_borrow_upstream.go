package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

// Borrow probes use the host transport and its provider profiles, exactly as
// ranxi fd1b5ee4's non-rotating path. Observation isolation belongs to the
// request context; copying transport configuration here would drift again.
func NewCodexGatewayBorrowProbeUpstream(upstream service.HTTPUpstream) service.CodexGatewayBorrowProbeUpstream {
	return upstream
}
