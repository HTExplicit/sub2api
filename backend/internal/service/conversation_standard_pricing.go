package service

// Exact international USD API prices verified 2026-10-04. These enter the
// ordinary fallback table; a dynamic catalog price remains authoritative.
// Tencent: https://intl.cloud.tencent.com/zh/document/product/1300/78937
// Qwen: https://www.alibabacloud.com/help/en/model-studio/qwen3-8-27b
//
//	https://www.alibabacloud.com/help/en/model-studio/qwen3-8-flash
//	https://www.alibabacloud.com/help/en/model-studio/model-pricing
//
// Meta: https://dev.meta.ai/docs/pricing-rate-limits
func addConversationStandardFallbackPricing(prices map[string]*ModelPricing) {
	for _, row := range []struct {
		name, provider                       string
		input, output, cacheRead, cacheWrite float64 // USD / million tokens
	}{
		{"hy3", "tencent", 0.132, 0.528, 0.033, 0},
		{"hy4-preview", "tencent", 0.834, 2.501, 0.042, 0},
		// The single cached-input field uses the implicit-cache rate. Explicit
		// cache creation has its own published rate; no TTL ladder is assumed.
		{"qwen3.8-27b", "qwen", 0.50, 3.00, 0.10, 0.625},
		{"qwen3.8-flash", "qwen", 0.15, 0.47, 0.016, 0.20},
		{"qwen3.8-omni-flash", "qwen", 0.15, 0.47, 0.016, 0},
		{"muse-spark-1.3", "meta", 1.25, 4.25, 0.15, 0},
	} {
		price := &ModelPricing{InputPricePerToken: row.input / 1e6, OutputPricePerToken: row.output / 1e6,
			CacheReadPricePerToken: row.cacheRead / 1e6, CacheCreationPricePerToken: row.cacheWrite / 1e6}
		prices[row.name] = price
		prices[row.provider+"/"+row.name] = price
	}
}
