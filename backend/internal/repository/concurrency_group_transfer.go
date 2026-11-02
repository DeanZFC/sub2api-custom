package repository

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

var moveUserGroupScript = redis.NewScript(`
redis.replicate_commands()
local now = tonumber(redis.call('TIME')[1])
local ttl, userMax, groupMax = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])
local id = ARGV[4]
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-ttl)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now-60)
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', now-ttl)
redis.call('ZREMRANGEBYSCORE', KEYS[5], '-inf', now-60)
local held = redis.call('ZSCORE', KEYS[1], id) ~= false
if not held and userMax > 0 and redis.call('ZCARD', KEYS[1])+redis.call('ZCARD', KEYS[2]) >= userMax then return {0, now} end
if groupMax > 0 and redis.call('ZSCORE', KEYS[4], id) == false and redis.call('ZCARD', KEYS[4])+redis.call('ZCARD', KEYS[5]) >= groupMax then return {0, now} end
redis.call('ZADD', KEYS[1], now, id)
redis.call('EXPIRE', KEYS[1], ttl)
if KEYS[3] ~= KEYS[4] then redis.call('ZREM', KEYS[3], id) end
if groupMax > 0 then
 redis.call('ZADD', KEYS[4], now, id)
 redis.call('EXPIRE', KEYS[4], ttl)
else
 redis.call('ZREM', KEYS[4], id)
end
return {1, now}
`)

func (c *concurrencyCache) MoveUserGroupSlot(ctx context.Context, userID, sourceID, targetID int64, userMax, groupMax int, requestID string) (bool, error) {
	if c == nil || c.rdb == nil || userID <= 0 || targetID <= 0 || requestID == "" {
		return false, fmt.Errorf("invalid user group concurrency transfer")
	}
	result, now, err := runScriptInt64Pair(ctx, c.rdb, moveUserGroupScript, []string{
		userSlotKey(userID), liveUserSlotKey(userID), userGroupSlotKey(userID, sourceID),
		userGroupSlotKey(userID, targetID), liveUserGroupSlotKey(userID, targetID),
	}, c.slotTTLSeconds, userMax, groupMax, requestID)
	if err == nil && result == 1 {
		c.touchActiveIndexAt(ctx, userActiveIndexKey, userID, now+int64(c.slotTTLSeconds))
	}
	return result == 1, err
}
