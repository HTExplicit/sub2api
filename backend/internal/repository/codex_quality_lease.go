package repository

import "time"

func (s *httpUpstreamService) HasCodexQualityConnection(id string, accountID int64, scope string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease := s.codexConnectionLeases[id]
	return lease != nil && lease.accountID == accountID && lease.scope == scope && time.Now().Before(lease.expiresAt)
}

// Close only the connection whose complete ownership tuple belongs to this
// diagnostic run. Shared pools and other accounts' leases are never touched.
func (s *httpUpstreamService) CloseCodexQualityConnection(id string, accountID int64, scope string) {
	s.mu.RLock()
	lease := s.codexConnectionLeases[id]
	s.mu.RUnlock()
	if lease != nil && lease.accountID == accountID && lease.scope == scope {
		s.removeCodexConnectionLease(id, lease)
	}
}
