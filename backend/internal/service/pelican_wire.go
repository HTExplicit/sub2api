package service

// The independent account-test endpoints share one Pelican runner,
// execution capacity and account leases.
func ProvidePelicanTestRunner(
	repo PelicanTestRepository,
	accounts AccountRepository,
	tests *AccountTestService,
	gateway *GatewayService,
	gemini *GeminiMessagesCompatService,
	concurrency *ConcurrencyService,
) *PelicanTestRunner {
	tests.SetPelicanBusinessServices(gateway, gemini, concurrency)
	runner := NewPelicanTestRunner(repo, tests, accounts)
	return runner
}
