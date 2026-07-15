// Package redisx holds the project's Redis wiring: connection setup, the
// position→evaluation cache, and a distributed rate limiter.
//
// Everything here is optional by design. Redis is a cache and a message bus, not
// a source of truth: if it is unreachable the platform must still play chess,
// just slower and without cross-replica fanout. So callers get a nil-safe client
// whose methods degrade instead of failing.
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Dial connects to Redis and verifies the connection. An empty addr returns
// (nil, nil): Redis is off, and every helper degrades to a no-op.
func Dial(ctx context.Context, addr string) (*redis.Client, error) {
	if addr == "" {
		return nil, nil
	}
	client := redis.NewClient(&redis.Options{
		Addr: addr,
		// Fail fast. A stalled cache lookup must never be slower than the engine
		// call it is meant to save.
		DialTimeout:  2 * time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		PoolSize:     20,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis %q: %w", addr, err)
	}
	return client, nil
}
