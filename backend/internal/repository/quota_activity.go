package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

type quotaActivityStore struct{ redis *redis.Client }

func NewQuotaActivityStore(client *redis.Client) service.QuotaActivityStore {
	return &quotaActivityStore{redis: client}
}

var quotaActivityScript = redis.NewScript(`
redis.call('HSETNX',KEYS[2],'epoch',ARGV[4])
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local expired=redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now)
if expired>0 then redis.call('HINCRBY',KEYS[2],'gaps',expired);redis.call('HINCRBY',KEYS[2],'revision',1) end
local action=ARGV[1]
local present=redis.call('ZSCORE',KEYS[1],ARGV[2])
if action=='begin' or action=='refresh' then
  if not present then
    redis.call('HINCRBY',KEYS[2],'revision',1)
    if action=='refresh' then redis.call('HINCRBY',KEYS[2],'gaps',1) end
  end
  redis.call('ZADD',KEYS[1],now+90000,ARGV[2])
elseif action=='finish' then
  redis.call('ZREM',KEYS[1],ARGV[2])
  redis.call('HINCRBY',KEYS[2],'revision',1)
  if ARGV[3]~='1' then redis.call('HINCRBY',KEYS[2],'gaps',1) end
end
return {redis.call('ZCARD',KEYS[1]),tonumber(redis.call('HGET',KEYS[2],'revision') or '0'),tonumber(redis.call('HGET',KEYS[2],'gaps') or '0'),redis.call('HGET',KEYS[2],'epoch')}
`)

func (s *quotaActivityStore) run(ctx context.Context, id int64, action, request string, complete bool) (service.QuotaActivityStamp, error) {
	prefix := fmt.Sprintf("quota-observation:{%d}:", id)
	settled := "0"
	if complete {
		settled = "1"
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return service.QuotaActivityStamp{}, err
	}
	values, err := quotaActivityScript.Run(ctx, s.redis, []string{prefix + "active", prefix + "clock"}, action, request, settled, hex.EncodeToString(nonce[:])).Slice()
	if err != nil {
		return service.QuotaActivityStamp{}, err
	}
	if len(values) != 4 {
		return service.QuotaActivityStamp{}, fmt.Errorf("invalid quota activity stamp")
	}
	active, okActive := values[0].(int64)
	revision, okRevision := values[1].(int64)
	gaps, okGaps := values[2].(int64)
	epoch, okEpoch := values[3].(string)
	if !okActive || !okRevision || !okGaps || !okEpoch || epoch == "" {
		return service.QuotaActivityStamp{}, fmt.Errorf("invalid quota activity types")
	}
	return service.QuotaActivityStamp{Active: active, Revision: revision, Gaps: gaps, Epoch: epoch}, nil
}
func (s *quotaActivityStore) Begin(ctx context.Context, id int64, request string) error {
	_, err := s.run(ctx, id, "begin", request, false)
	return err
}
func (s *quotaActivityStore) Refresh(ctx context.Context, id int64, request string) error {
	_, err := s.run(ctx, id, "refresh", request, false)
	return err
}
func (s *quotaActivityStore) Finish(ctx context.Context, id int64, request string, complete bool) error {
	_, err := s.run(ctx, id, "finish", request, complete)
	return err
}
func (s *quotaActivityStore) Read(ctx context.Context, id int64) (service.QuotaActivityStamp, error) {
	return s.run(ctx, id, "read", "", false)
}
