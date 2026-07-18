package eventlog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Stream reads and writes per-game move event streams backed by Redis Streams.
type Stream struct {
	client *redis.Client
	log    *slog.Logger
}

// NewStream builds a Stream over an existing Redis client. A nil client yields a
// disabled Stream whose methods are no-ops, mirroring redisx's degrade-don't-fail
// contract.
func NewStream(client *redis.Client, log *slog.Logger) *Stream {
	return &Stream{client: client, log: log}
}

// Enabled reports whether Redis is configured.
func (s *Stream) Enabled() bool { return s != nil && s.client != nil }

// key namespaces one stream per game. The `:events` suffix keeps it distinct
// from the gateway's `game:{id}` pub/sub channel, which serves a different
// (at-most-once, full-snapshot) purpose.
func key(gameID string) string { return "game:" + gameID + ":events" }

// Append writes one move event to its game's stream and returns the assigned
// stream ID. A chess game is bounded to a few hundred plies, so the stream needs
// no trimming; it is expired wholesale when the game is cleaned up.
func (s *Stream) Append(ctx context.Context, e MoveEvent) (string, error) {
	if !s.Enabled() {
		return "", nil
	}
	id, err := s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key(e.GameID),
		Values: e.fields(),
	}).Result()
	if err != nil {
		return "", fmt.Errorf("xadd game %q: %w", e.GameID, err)
	}
	return id, nil
}

// Replay returns the game's events in order. afterID is exclusive: pass "" to
// read from the beginning (state rebuild), or the last seen ID to read only what
// a reconnecting consumer missed.
func (s *Stream) Replay(ctx context.Context, gameID, afterID string) ([]Entry, error) {
	if !s.Enabled() {
		return nil, nil
	}
	start := "-"
	if afterID != "" {
		start = "(" + afterID // exclusive lower bound
	}
	msgs, err := s.client.XRange(ctx, key(gameID), start, "+").Result()
	if err != nil {
		return nil, fmt.Errorf("xrange game %q: %w", gameID, err)
	}
	return s.decode(gameID, msgs), nil
}

// Read blocks up to `block` for events after lastID, for live tailing. Pass "$"
// (or "") as lastID to receive only events that arrive after the call. It
// returns (nil, nil) when the block elapses with nothing new, so callers loop.
func (s *Stream) Read(ctx context.Context, gameID, lastID string, block time.Duration) ([]Entry, error) {
	if !s.Enabled() {
		return nil, nil
	}
	if lastID == "" {
		lastID = "$"
	}
	res, err := s.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{key(gameID), lastID},
		Block:   block,
		Count:   128,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil // block elapsed, no new entries
	}
	if err != nil {
		return nil, fmt.Errorf("xread game %q: %w", gameID, err)
	}
	if len(res) == 0 {
		return nil, nil
	}
	return s.decode(gameID, res[0].Messages), nil
}

// decode turns raw stream messages into typed entries, skipping (and logging)
// any malformed one rather than failing the whole batch.
func (s *Stream) decode(gameID string, msgs []redis.XMessage) []Entry {
	out := make([]Entry, 0, len(msgs))
	for _, msg := range msgs {
		fields := make(map[string]string, len(msg.Values))
		for k, v := range msg.Values {
			if str, ok := v.(string); ok {
				fields[k] = str
			}
		}
		ev, err := decodeEvent(fields)
		if err != nil {
			if s.log != nil {
				s.log.Warn("skipping malformed event", "game_id", gameID, "stream_id", msg.ID, "error", err)
			}
			continue
		}
		out = append(out, Entry{ID: msg.ID, Event: ev})
	}
	return out
}
