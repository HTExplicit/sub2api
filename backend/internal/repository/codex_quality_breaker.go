package repository

import "context"

// A retained marker/claim is an ordinary half-open breaker. Diagnostics wait
// for its ordinary owner to recover it, rather than claiming or deleting it.
func (c *gatewayCache) CodexQualityRuntimeBlocked(ctx context.Context, accountID int64, models []string) (bool, error) {
	var keys []string
	for _, model := range normalizeOpenAIRuntimeBreakerModels(models) {
		base := openAIRuntimeBreakerBaseKey(accountID, model)
		keys = append(keys, base+":block", base+":marker", base+":claim")
	}
	if len(keys) == 0 {
		return false, nil
	}
	count, err := c.rdb.Exists(ctx, keys...).Result()
	return count > 0, err
}
