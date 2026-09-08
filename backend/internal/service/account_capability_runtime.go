package service

import "context"

// Capability work is always administrator-initiated. Startup only recovers the
// durable queue; no discovery or paid probe is created automatically.
func ProvideAccountCapabilityService(repo AccountCapabilityRepository, accounts AccountRepository, executor AccountCapabilityExecutor) (*AccountCapabilityService, error) {
	svc := NewAccountCapabilityService(repo, accounts, executor)
	if err := svc.Start(context.Background()); err != nil {
		return nil, err
	}
	return svc, nil
}
