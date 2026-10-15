package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const loginProtectionPrefix = "login_protection:"

// All transitions use Redis time and an atomic script. Existing bans are read
// without refreshing TTL, including failures already in flight when banned.
var loginProtectionFailureScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local existing = redis.call('GET', KEYS[2])
if existing then return {existing, 0} end
local window = tonumber(ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-window)
redis.call('ZADD', KEYS[1], now, ARGV[4])
redis.call('PEXPIRE', KEYS[1], window)
if redis.call('ZCARD', KEYS[1]) <= tonumber(ARGV[2]) then return {'', 0} end
local expires = now + tonumber(ARGV[3])
local record = tostring(now) .. ':' .. tostring(expires)
redis.call('SET', KEYS[2], record, 'PX', ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now)
redis.call('ZADD', KEYS[3], expires, ARGV[5])
redis.call('DEL', KEYS[1])
return {record, 1}
`)

var loginProtectionUnbanScript = redis.NewScript(`
redis.call('DEL', KEYS[1], KEYS[2])
redis.call('ZREM', KEYS[3], ARGV[1])
return 1
`)

var loginProtectionListScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
local total = redis.call('ZCARD', KEYS[1])
local ips = redis.call('ZRANGE', KEYS[1], ARGV[1], ARGV[2])
local result = {total}
for _, ip in ipairs(ips) do
  local record = redis.call('GET', ARGV[3] .. ip)
  if record then table.insert(result, ip); table.insert(result, record) end
end
return result
`)

type loginProtectionStore struct{ client *redis.Client }

func NewLoginProtectionStore(client *redis.Client) service.LoginProtectionStore {
	return &loginProtectionStore{client: client}
}

func (s *loginProtectionStore) Check(ctx context.Context, ip string) (*service.LoginIPBan, error) {
	value, err := s.client.Get(ctx, loginProtectionPrefix+"ban:"+ip).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseLoginIPBan(ip, value)
}

func (s *loginProtectionStore) RecordFailure(ctx context.Context, ip string, settings service.LoginProtectionSettings) (*service.LoginIPBan, bool, error) {
	values, err := loginProtectionFailureScript.Run(ctx, s.client, []string{loginProtectionPrefix + "failures:" + ip, loginProtectionPrefix + "ban:" + ip, loginProtectionPrefix + "bans"},
		int64(settings.WindowMinutes)*60000, settings.MaxFailures, int64(settings.BanHours)*3600000, uuid.NewString(), ip).Slice()
	if err != nil {
		return nil, false, err
	}
	if len(values) != 2 {
		return nil, false, fmt.Errorf("invalid login protection result")
	}
	value, ok := values[0].(string)
	if !ok {
		return nil, false, fmt.Errorf("invalid login protection record")
	}
	if value == "" {
		return nil, false, nil
	}
	ban, err := parseLoginIPBan(ip, value)
	return ban, values[1] == int64(1), err
}

func (s *loginProtectionStore) Unban(ctx context.Context, ip string) error {
	return loginProtectionUnbanScript.Run(ctx, s.client,
		[]string{loginProtectionPrefix + "failures:" + ip, loginProtectionPrefix + "ban:" + ip, loginProtectionPrefix + "bans"}, ip).Err()
}

func (s *loginProtectionStore) List(ctx context.Context, page, size int) ([]service.LoginIPBan, int64, error) {
	if page < 1 || size < 1 || size > 100 {
		return nil, 0, fmt.Errorf("invalid ban pagination")
	}
	offset := int64(page-1) * int64(size)
	values, err := loginProtectionListScript.Run(ctx, s.client, []string{loginProtectionPrefix + "bans"}, offset, offset+int64(size)-1, loginProtectionPrefix+"ban:").Slice()
	if err != nil {
		return nil, 0, err
	}
	if len(values) == 0 || len(values)%2 != 1 {
		return nil, 0, fmt.Errorf("invalid ban listing")
	}
	total, ok := values[0].(int64)
	if !ok {
		return nil, 0, fmt.Errorf("invalid ban count")
	}
	items := make([]service.LoginIPBan, 0, size)
	for index := 1; index < len(values); index += 2 {
		ip, ipOK := values[index].(string)
		value, valueOK := values[index+1].(string)
		if !ipOK || !valueOK {
			return nil, 0, fmt.Errorf("invalid ban entry")
		}
		ban, err := parseLoginIPBan(ip, value)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *ban)
	}
	return items, total, nil
}

func parseLoginIPBan(ip, value string) (*service.LoginIPBan, error) {
	var startText, endText string
	for index, char := range value {
		if char == ':' {
			startText, endText = value[:index], value[index+1:]
			break
		}
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid ban start time: %w", err)
	}
	end, err := strconv.ParseInt(endText, 10, 64)
	if err != nil || end <= start {
		return nil, fmt.Errorf("invalid ban expiry")
	}
	return &service.LoginIPBan{IP: ip, BlockedAt: time.UnixMilli(start).UTC(), ExpiresAt: time.UnixMilli(end).UTC()}, nil
}
