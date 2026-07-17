package redisx

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// limiterTimeout bounds the Redis round-trip in Allow. A healthy call is
// sub-millisecond; this is loose enough never to reject a live Redis under load,
// tight enough that an unreachable Redis fails open almost instantly instead of
// adding its dial timeout to every request.
const limiterTimeout = 50 * time.Millisecond

// tokenBucket refills a bucket lazily and spends one token per request.
//
// Why a token bucket rather than a counter per fixed window: a fixed window lets
// a caller spend a full quota at the end of one window and another immediately
// at the start of the next, so the real burst is double the limit. A bucket
// refills continuously, so the average rate holds everywhere.
//
// It runs as a Lua script because read-refill-write must be atomic; done in Go
// it would be a race between replicas, which is exactly what a *distributed*
// limiter must not be.
var tokenBucket = redis.NewScript(`
local key      = KEYS[1]
local rate     = tonumber(ARGV[1])  -- tokens per second
local capacity = tonumber(ARGV[2])  -- burst size
local now      = tonumber(ARGV[3])  -- unix millis
local cost     = tonumber(ARGV[4])

local bucket = redis.call('HMGET', key, 'tokens', 'ts')
local tokens = tonumber(bucket[1])
local ts     = tonumber(bucket[2])

-- A first-time caller starts with a full bucket.
if tokens == nil or ts == nil then
  tokens = capacity
  ts = now
end

-- Refill for the time elapsed since we last looked.
local elapsed = math.max(0, now - ts) / 1000.0
tokens = math.min(capacity, tokens + elapsed * rate)

local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
end

redis.call('HSET', key, 'tokens', tokens, 'ts', now)
-- Expire an idle bucket once it would have refilled anyway: keeping it would
-- leak a key per caller forever, and dropping it is equivalent to a full bucket.
redis.call('PEXPIRE', key, math.ceil((capacity / rate) * 1000) + 1000)

return {allowed, math.floor(tokens)}
`)

// Limiter is a distributed token-bucket rate limiter.
//
// A nil Limiter allows everything, so callers need no branch when Redis is off.
type Limiter struct {
	client   *redis.Client
	rate     float64 // tokens per second
	capacity int     // burst
}

// NewLimiter builds a limiter allowing `rate` requests per second with bursts up
// to `capacity`. A nil client disables limiting.
func NewLimiter(client *redis.Client, rate float64, capacity int) *Limiter {
	return &Limiter{client: client, rate: rate, capacity: capacity}
}

// Allow reports whether the caller identified by key may proceed, and how many
// tokens remain.
//
// On a Redis error it allows the request: a limiter that fails closed would turn
// a cache outage into a full outage. The trade is deliberate — availability over
// strict enforcement for a chess API.
//
// The Redis call is bounded by limiterTimeout so that "fails open" is fast: an
// unreachable Redis must degrade to "no rate limiting" in milliseconds, not
// stall every request on the client's dial timeout. (Chaos testing caught this:
// without the bound, scaling Redis to zero hung even the health endpoint for
// seconds, because every request passes through the limiter.)
func (l *Limiter) Allow(ctx context.Context, key string) (allowed bool, remaining int) {
	// rate <= 0 disables limiting (used to measure raw throughput under load).
	if l == nil || l.client == nil || l.rate <= 0 {
		return true, l.burst()
	}
	rctx, cancel := context.WithTimeout(ctx, limiterTimeout)
	defer cancel()
	res, err := tokenBucket.Run(rctx, l.client,
		[]string{"ratelimit:" + key},
		l.rate, l.capacity, time.Now().UnixMilli(), 1,
	).Slice()
	if err != nil || len(res) != 2 {
		return true, l.burst()
	}
	ok, _ := res[0].(int64)
	left, _ := res[1].(int64)
	return ok == 1, int(left)
}

func (l *Limiter) burst() int {
	if l == nil {
		return 0
	}
	return l.capacity
}
