// Package engine is a thin gRPC client for the engine-worker's EngineService.
package engine

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	enginev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/engine/v1"
)

// Client wraps an EngineService gRPC connection.
type Client struct {
	conn   *grpc.ClientConn
	client enginev1.EngineServiceClient
}

// Dial connects to the engine-worker at addr.
func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial engine %q: %w", addr, err)
	}
	return &Client{conn: conn, client: enginev1.NewEngineServiceClient(conn)}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }

// BestMove asks the engine for its best move in the given position. depth 0 lets
// the engine-worker apply its default.
func (c *Client) BestMove(ctx context.Context, fen string, depth int) (string, error) {
	resp, err := c.client.Analyze(ctx, &enginev1.AnalyzeRequest{
		Fen:   fen,
		Depth: uint32(depth),
	})
	if err != nil {
		return "", fmt.Errorf("engine analyze: %w", err)
	}
	return resp.GetBestmove(), nil
}
