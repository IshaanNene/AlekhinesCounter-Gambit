package redisx

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// testClient connects to the Redis from docker-compose, skipping when absent so
// the suite still runs on a machine without it.
func testClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("ACG_REDIS_ADDR")
	if addr == "" {
		// 6380, not 6379: see docker-compose.yml — a local Redis on the default
		// port would answer instead, without the Stack modules.
		addr = "localhost:6380"
	}
	ctx := context.Background()
	client, err := Dial(ctx, addr)
	if err != nil {
		t.Skipf("redis unavailable at %s: %v", addr, err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestDialEmptyAddrDisablesRedis(t *testing.T) {
	client, err := Dial(context.Background(), "")
	if err != nil {
		t.Errorf("empty addr should not error, got %v", err)
	}
	if client != nil {
		t.Error("empty addr should yield a nil client")
	}
}

// A nil cache must be usable: callers should not need a branch when Redis is off.
func TestNilCacheDegradesQuietly(t *testing.T) {
	c := NewEvalCache(nil)
	if c.Enabled() {
		t.Error("cache with nil client should report disabled")
	}
	if _, ok := c.Get(context.Background(), "fen", 10); ok {
		t.Error("disabled cache should never hit")
	}
	c.Put(context.Background(), "fen", 10, &Eval{BestMove: "e2e4"}) // must not panic
}

func TestEvalCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	c := NewEvalCache(client)

	fen := "test-fen-" + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() { client.Del(ctx, evalKey(fen, 12)) })

	if _, ok := c.Get(ctx, fen, 12); ok {
		t.Fatal("expected a miss for an unseen position")
	}

	want := &Eval{BestMove: "e2e4", ScoreCP: 31, Depth: 12, PV: []string{"e2e4", "e7e5"}}
	c.Put(ctx, fen, 12, want)

	got, ok := c.Get(ctx, fen, 12)
	if !ok {
		t.Fatal("expected a hit after Put")
	}
	if got.BestMove != want.BestMove || got.ScoreCP != want.ScoreCP || len(got.PV) != 2 {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}

	hits, misses, _ := c.Stats()
	if hits != 1 || misses != 1 {
		t.Errorf("stats = %d hits / %d misses, want 1/1", hits, misses)
	}
}

// The same position searched to a different depth is a different answer and must
// not share a cache entry.
func TestEvalCacheKeysOnDepth(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	c := NewEvalCache(client)

	fen := "depth-fen-" + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() {
		client.Del(ctx, evalKey(fen, 5), evalKey(fen, 20))
	})

	c.Put(ctx, fen, 5, &Eval{BestMove: "a2a3", Depth: 5})
	if _, ok := c.Get(ctx, fen, 20); ok {
		t.Error("depth 20 must not read depth 5's entry")
	}

	shallow, ok := c.Get(ctx, fen, 5)
	if !ok || shallow.BestMove != "a2a3" {
		t.Errorf("depth 5 entry = %+v, want a2a3", shallow)
	}
}

func TestLimiterAllowsBurstThenRefuses(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)

	// 1 token/sec, burst 5: the 6th request within a second must be refused.
	l := NewLimiter(client, 1, 5)
	key := "test-burst-" + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() { client.Del(ctx, "ratelimit:"+key) })

	for i := 1; i <= 5; i++ {
		if allowed, _ := l.Allow(ctx, key); !allowed {
			t.Fatalf("request %d should be allowed within the burst", i)
		}
	}
	if allowed, remaining := l.Allow(ctx, key); allowed {
		t.Errorf("request 6 should exceed the burst (remaining=%d)", remaining)
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)

	// 20 tokens/sec, burst 2: drained in 2, one token back after ~50ms.
	l := NewLimiter(client, 20, 2)
	key := "test-refill-" + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() { client.Del(ctx, "ratelimit:"+key) })

	l.Allow(ctx, key)
	l.Allow(ctx, key)
	if allowed, _ := l.Allow(ctx, key); allowed {
		t.Fatal("bucket should be drained")
	}

	time.Sleep(200 * time.Millisecond) // ≥4 tokens' worth of refill
	if allowed, _ := l.Allow(ctx, key); !allowed {
		t.Error("bucket should have refilled")
	}
}

// Separate callers must not share a bucket.
func TestLimiterIsolatesKeys(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)

	l := NewLimiter(client, 1, 2)
	a := "iso-a-" + time.Now().Format(time.RFC3339Nano)
	b := "iso-b-" + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() { client.Del(ctx, "ratelimit:"+a, "ratelimit:"+b) })

	l.Allow(ctx, a)
	l.Allow(ctx, a)
	if allowed, _ := l.Allow(ctx, a); allowed {
		t.Fatal("caller a should be drained")
	}
	if allowed, _ := l.Allow(ctx, b); !allowed {
		t.Error("caller b must have its own bucket")
	}
}

// A nil limiter allows everything, so callers need no branch when Redis is off.
func TestNilLimiterAllows(t *testing.T) {
	var l *Limiter
	if allowed, _ := l.Allow(context.Background(), "anything"); !allowed {
		t.Error("nil limiter must allow")
	}
	l2 := NewLimiter(nil, 1, 1)
	if allowed, _ := l2.Allow(context.Background(), "anything"); !allowed {
		t.Error("limiter with nil client must allow")
	}
}
