package openingbook

import (
	"testing"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
)

// The seed lines must all be legal, or Build (and thus Seed) fails.
func TestSeedBuildsAndCoversOpenings(t *testing.T) {
	b := Seed()
	if b.Len() == 0 {
		t.Fatal("seed book is empty")
	}
	// The start position must offer the two mainline first moves.
	moves := map[string]bool{}
	for i := 0; i < 200; i++ {
		m, ok := b.Move(chess.StartFEN)
		if !ok {
			t.Fatal("no book move for the start position")
		}
		moves[m] = true
	}
	for _, want := range []string{"e2e4", "d2d4"} {
		if !moves[want] {
			t.Errorf("expected the book to play %s from the start over many draws; got %v", want, moves)
		}
	}
}

// Every book move must be legal in the position it is offered for — a book that
// suggests an illegal move would break the engine that trusts it.
func TestEveryBookMoveIsLegal(t *testing.T) {
	b := Seed()
	for key, moves := range b.positions {
		// Reconstruct a full FEN from the position key (counters do not affect legality).
		fen := key + " 0 1"
		board, err := chess.ParseFEN(fen)
		if err != nil {
			t.Fatalf("book key %q is not a valid position: %v", key, err)
		}
		for _, m := range moves {
			if _, err := board.ApplyUCI(m.UCI); err != nil {
				t.Errorf("illegal book move %s in %q: %v", m.UCI, key, err)
			}
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	b := Seed()
	blob, err := b.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(blob)
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != b.Len() {
		t.Errorf("round-trip changed position count: %d -> %d", b.Len(), got.Len())
	}
	if _, ok := got.Move(chess.StartFEN); !ok {
		t.Error("round-tripped book lost the start position")
	}
}

// A position outside the book must report a miss, so the engine searches instead.
func TestUnknownPositionMisses(t *testing.T) {
	b := Seed()
	// A contrived endgame that no opening line reaches.
	if _, ok := b.Move("8/8/8/4k3/8/4K3/8/8 w - -"); ok {
		t.Error("expected a miss for an endgame position not in the book")
	}
}
