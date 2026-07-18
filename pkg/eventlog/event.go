// Package eventlog is the durable, ordered per-game move event stream that
// underpins both live spectator fanout and session-state recovery.
//
// Each game has its own Redis Stream (`game:{id}:events`) of MoveEvents in ply
// order. Two independent readers rely on it:
//
//   - the fanout tier tails the stream and pushes deltas to spectators, replaying
//     missed entries on reconnect, so a client never sees a frozen board;
//   - a session-manager node that (re)acquires a game rebuilds its live clock and
//     turn by replaying the stream, so a node death loses nothing ephemeral.
//
// Events reach the stream via a transactional outbox (see pkg/store + Relay), so
// an event is durable exactly when its move is. Like redisx, the whole package is
// nil-safe: with Redis unconfigured every method degrades to a no-op and the
// platform still plays chess, just without live fanout.
package eventlog

import (
	"fmt"
	"strconv"
)

// MoveEvent is the durable record that a move landed in a game. It carries
// enough to reconstruct the move downstream without re-reading Postgres.
type MoveEvent struct {
	GameID    string
	Ply       int
	UCI       string
	FENAfter  string
	Status    string // game status after this move (IN_PROGRESS | WHITE_WON | ...)
	EndReason string // set only when this move ended the game
	Ended     bool   // whether this move terminated the game
}

// Entry is a MoveEvent as stored in the stream, tagged with its stream ID so a
// consumer can resume from exactly where it left off (Redis Streams IDs are
// monotonic per stream).
type Entry struct {
	ID    string
	Event MoveEvent
}

// fields flattens the event into the field/value pairs of a stream entry.
func (e MoveEvent) fields() map[string]any {
	return map[string]any{
		"game_id":    e.GameID,
		"ply":        strconv.Itoa(e.Ply),
		"uci":        e.UCI,
		"fen_after":  e.FENAfter,
		"status":     e.Status,
		"end_reason": e.EndReason,
		"ended":      strconv.FormatBool(e.Ended),
	}
}

// decodeEvent rebuilds a MoveEvent from a stream entry's fields. The stream is
// the source of game_id, so a malformed or missing ply is the only hard error;
// absent optional fields decode to their zero values.
func decodeEvent(m map[string]string) (MoveEvent, error) {
	ply, err := strconv.Atoi(m["ply"])
	if err != nil {
		return MoveEvent{}, fmt.Errorf("decode ply %q: %w", m["ply"], err)
	}
	ended, _ := strconv.ParseBool(m["ended"])
	return MoveEvent{
		GameID:    m["game_id"],
		Ply:       ply,
		UCI:       m["uci"],
		FENAfter:  m["fen_after"],
		Status:    m["status"],
		EndReason: m["end_reason"],
		Ended:     ended,
	}, nil
}
