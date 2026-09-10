package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	managedModelAffinityPrefix      = "managed_model_affinity:v2:"
	managedModelAffinityBatchMax    = 1024
	managedModelAffinitySelectorMax = 256
	managedModelAffinityTTLMax      = 24 * time.Hour
)

var (
	_ service.ManagedModelAffinityCache = (*gatewayCache)(nil)

	errManagedModelAffinityInput       = errors.New("invalid managed model affinity cache input")
	errManagedModelAffinityCorrupt     = errors.New("invalid managed model affinity cache record")
	errManagedModelAffinityUnavailable = errors.New("managed model affinity cache unavailable")
)

// Read the whole batch before writing: Redis scripts are atomic but do not
// roll back writes if a later GET encounters a key with the wrong Redis type.
// Account IDs remain decimal strings, avoiding Lua's floating-point precision.
const bindManagedModelAffinityScript = `
local values = {}
for index, key in ipairs(KEYS) do
  local current = redis.call('GET', key)
  if current and current ~= ARGV[1] then
    values[index] = '!'
  else
    values[index] = ARGV[1]
  end
end
for index, key in ipairs(KEYS) do
  redis.call('SET', key, values[index], 'PX', ARGV[2])
end
return #KEYS
`

func managedModelAffinityKeys(hashes []string) ([]string, error) {
	if len(hashes) > managedModelAffinityBatchMax {
		return nil, errManagedModelAffinityInput
	}
	keys := make([]string, len(hashes))
	for index, digest := range hashes {
		if len(digest) != 64 {
			return nil, errManagedModelAffinityInput
		}
		for _, char := range []byte(digest) {
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				return nil, errManagedModelAffinityInput
			}
		}
		keys[index] = managedModelAffinityPrefix + digest
	}
	return keys, nil
}

func validManagedModelAffinitySelector(selector string) bool {
	return selector != "" && len(selector) <= managedModelAffinitySelectorMax && !strings.ContainsAny(selector, "\r\n")
}

func encodeManagedModelAffinity(binding service.ManagedModelAffinityBinding) (string, error) {
	if binding.Ambiguous {
		return "!", nil
	}
	if binding.AccountID <= 0 || !validManagedModelAffinitySelector(binding.BranchSelector) {
		return "", errManagedModelAffinityInput
	}
	return strconv.FormatInt(binding.AccountID, 10) + "\n" + binding.BranchSelector, nil
}

func decodeManagedModelAffinity(value string) (service.ManagedModelAffinityBinding, error) {
	if value == "!" {
		return service.ManagedModelAffinityBinding{Ambiguous: true}, nil
	}
	if len(value) > 20+managedModelAffinitySelectorMax {
		return service.ManagedModelAffinityBinding{}, errManagedModelAffinityCorrupt
	}
	account, selector, found := strings.Cut(value, "\n")
	accountID, err := strconv.ParseInt(account, 10, 64)
	if !found || err != nil || accountID <= 0 || strconv.FormatInt(accountID, 10) != account || !validManagedModelAffinitySelector(selector) {
		return service.ManagedModelAffinityBinding{}, errManagedModelAffinityCorrupt
	}
	return service.ManagedModelAffinityBinding{AccountID: accountID, BranchSelector: selector}, nil
}

// GetManagedModelAffinity reads only scoped digests. Redis nulls are misses;
// malformed string values abort the batch instead of returning a partial pin.
func (c *gatewayCache) GetManagedModelAffinity(ctx context.Context, hashes []string) (map[string]service.ManagedModelAffinityBinding, error) {
	keys, err := managedModelAffinityKeys(hashes)
	if err != nil {
		return nil, err
	}
	result := make(map[string]service.ManagedModelAffinityBinding, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	if c == nil || c.rdb == nil {
		return nil, errManagedModelAffinityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	if len(values) != len(keys) {
		return nil, errManagedModelAffinityCorrupt
	}
	for index, raw := range values {
		if raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return nil, errManagedModelAffinityCorrupt
		}
		binding, err := decodeManagedModelAffinity(value)
		if err != nil {
			return nil, err
		}
		result[hashes[index]] = binding
	}
	return result, nil
}

// BindManagedModelAffinity uses one EVAL for the entire batch. An observation
// on another route turns an existing pin into an ambiguity tombstone; neither
// the old nor the new route may replace that tombstone before its expiry.
func (c *gatewayCache) BindManagedModelAffinity(ctx context.Context, hashes []string, binding service.ManagedModelAffinityBinding, ttl time.Duration) error {
	keys, err := managedModelAffinityKeys(hashes)
	if err != nil {
		return err
	}
	value, err := encodeManagedModelAffinity(binding)
	if err != nil {
		return err
	}
	if ttl <= 0 || ttl > managedModelAffinityTTLMax {
		return errManagedModelAffinityInput
	}
	// Redis has millisecond precision. A positive sub-millisecond duration must
	// not truncate to PX 0, nor may rounding exceed the 24-hour upper bound.
	ttlMilliseconds := int64((ttl + time.Millisecond - 1) / time.Millisecond)
	if len(keys) == 0 {
		return nil
	}
	if c == nil || c.rdb == nil {
		return errManagedModelAffinityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.rdb.Eval(ctx, bindManagedModelAffinityScript, keys, value, ttlMilliseconds).Err()
}
