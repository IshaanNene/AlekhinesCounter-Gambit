// Package engine is a thin gRPC client for the engine-worker's EngineService,
// with a read-through evaluation cache in front of it.
package engine

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	enginev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/engine/v1"
)

// Client wraps an EngineService gRPC connection.
type Client struct {
	conn   *grpc.ClientConn
	client enginev1.EngineServiceClient
	cache  *redisx.EvalCache
	log    *slog.Logger
}

// Dial connects to the engine-worker at addr. cache may be nil or disabled, in
// which case every request goes to the engine.
func Dial(addr string, cache *redisx.EvalCache, log *slog.Logger) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, fmt.Errorf("dial engine %q: %w", addr, err)
	}
	return &Client{
		conn:   conn,
		client: enginev1.NewEngineServiceClient(conn),
		cache:  cache,
		log:    log,
	}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }

// BestMove asks the engine for its best move in the given position. depth 0 lets
// the engine-worker apply its default.
func (c *Client) BestMove(ctx context.Context, fen string, depth int) (string, error) {
	e, err := c.Analyze(ctx, fen, depth)
	if err != nil {
		return "", err
	}
	return e.BestMove, nil
}

// Analyze evaluates a position, serving from cache when possible.
//
// A position at a fixed depth always evaluates the same, so this is a pure
// function the cache can memoise. That matters because openings and common
// middlegames recur across every game on the platform: the cache turns a
// second of engine search into a millisecond lookup, and lets the same worker
// pool serve far more games.
//
// Only cached when a concrete depth was requested: with depth 0 the worker picks
// its own, so the answer is not reproducible under that key.
func (c *Client) Analyze(ctx context.Context, fen string, depth int) (*redisx.Eval, error) {
	cacheable := depth > 0 && c.cache.Enabled()

	if cacheable {
		if hit, ok := c.cache.Get(ctx, fen, uint32(depth)); ok {
			return hit, nil
		}
	}

	resp, err := c.client.Analyze(ctx, &enginev1.AnalyzeRequest{
		Fen:   fen,
		Depth: uint32(depth),
	})
	if err != nil {
		return nil, fmt.Errorf("engine analyze: %w", err)
	}

	e := &redisx.Eval{
		BestMove: resp.GetBestmove(),
		ScoreCP:  resp.GetScoreCp(),
		Mate:     resp.GetMate(),
		MateIn:   resp.GetMateIn(),
		Depth:    resp.GetDepth(),
		PV:       resp.GetPv(),
	}
	if cacheable {
		c.cache.Put(ctx, fen, uint32(depth), e)
	}
	return e, nil
}

// CacheStats exposes cache counters for logging.
func (c *Client) CacheStats() (hits, misses int64, rate float64) {
	h, m, _ := c.cache.Stats()
	return h, m, c.cache.HitRate()
}
