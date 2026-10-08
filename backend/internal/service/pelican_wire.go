package service

// One runner serves both the historical borrowed-cache endpoints and the
// independent account test endpoints, sharing admission and account leases.
func ProvidePelicanTestRunner(
	repo CodexGatewayBorrowTestRepository,
	core *CodexGatewayBorrowService,
	accounts AccountRepository,
	tests *AccountTestService,
	gateway *GatewayService,
	gemini *GeminiMessagesCompatService,
	concurrency *ConcurrencyService,
) *CodexGatewayBorrowTestRunner {
	tests.SetCodexGatewayBorrowService(core)
	tests.SetPelicanBusinessServices(gateway, gemini, concurrency)
	runner := NewCodexGatewayBorrowTestRunner(repo, core, accounts)
	runner.SetStandaloneGenerator(tests)
	return runner
}
