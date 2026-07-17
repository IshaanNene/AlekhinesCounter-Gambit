// Package openingbook is a small, self-contained opening book: a map from a
// position to weighted candidate moves, with weighted-random selection so engine
// play varies its openings instead of repeating one line every game.
//
// The book is a plain JSON file. It lives in object storage (MinIO bucket
// "books") and is loaded by the engine workers at startup — so the same book is
// served to every replica, and swapping it is a file upload, not a redeploy.
package openingbook

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
)

// positionKey reduces a FEN to placement, side, castling, and en passant,
// dropping the move counters so a transposition maps to one entry — the same
// reduction the opening explorer uses.
func positionKey(fen string) string {
	f := strings.Fields(fen)
	if len(f) >= 4 {
		return strings.Join(f[:4], " ")
	}
	return fen
}

// Move is a candidate reply with a selection weight.
type Move struct {
	UCI    string `json:"uci"`
	Weight int    `json:"weight"`
}

// Book maps a position key to its weighted candidate moves.
type Book struct {
	positions map[string][]Move

	mu  sync.Mutex // guards rng; Move is called concurrently by the gRPC server
	rng *rand.Rand
}

type wireBook struct {
	Positions map[string][]Move `json:"positions"`
}

func newRNG() *rand.Rand { return rand.New(rand.NewSource(time.Now().UnixNano())) }

// Load parses a book from its JSON serialization.
func Load(data []byte) (*Book, error) {
	var w wireBook
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parse opening book: %w", err)
	}
	if w.Positions == nil {
		w.Positions = map[string][]Move{}
	}
	return &Book{positions: w.Positions, rng: newRNG()}, nil
}

// Build constructs a book by walking each opening line move by move. Every
// position along a line gains the move played from it, so a move shared by many
// lines accrues weight and is played more often — popularity as weight. Every
// move is validated against the rules while walking, so an illegal line is a
// build error rather than a bad book.
func Build(lines [][]string) (*Book, error) {
	counts := map[string]map[string]int{} // key -> uci -> weight
	for _, line := range lines {
		b, err := chess.ParseFEN(chess.StartFEN)
		if err != nil {
			return nil, err
		}
		for _, uci := range line {
			key := positionKey(b.FEN())
			if counts[key] == nil {
				counts[key] = map[string]int{}
			}
			counts[key][uci]++
			b, err = b.ApplyUCI(uci)
			if err != nil {
				return nil, fmt.Errorf("opening line move %q: %w", uci, err)
			}
		}
	}

	positions := make(map[string][]Move, len(counts))
	for key, mm := range counts {
		moves := make([]Move, 0, len(mm))
		for uci, w := range mm {
			moves = append(moves, Move{UCI: uci, Weight: w})
		}
		// Heaviest first, then by UCI, so the serialized book is deterministic.
		sort.Slice(moves, func(i, j int) bool {
			if moves[i].Weight != moves[j].Weight {
				return moves[i].Weight > moves[j].Weight
			}
			return moves[i].UCI < moves[j].UCI
		})
		positions[key] = moves
	}
	return &Book{positions: positions, rng: newRNG()}, nil
}

// Marshal serializes the book to indented JSON. encoding/json emits map keys
// sorted, so the same book always marshals identically.
func (b *Book) Marshal() ([]byte, error) {
	return json.MarshalIndent(wireBook{Positions: b.positions}, "", "  ")
}

// Move returns a weighted-random book move for the position, or ok=false when
// the position is not in the book (the caller should then search).
func (b *Book) Move(fen string) (string, bool) {
	if b == nil {
		return "", false
	}
	moves := b.positions[positionKey(fen)]
	if len(moves) == 0 {
		return "", false
	}
	total := 0
	for _, m := range moves {
		total += m.Weight
	}
	if total <= 0 {
		return moves[0].UCI, true
	}
	b.mu.Lock()
	n := b.rng.Intn(total)
	b.mu.Unlock()
	for _, m := range moves {
		if n < m.Weight {
			return m.UCI, true
		}
		n -= m.Weight
	}
	return moves[len(moves)-1].UCI, true
}

// Len reports how many positions the book covers.
func (b *Book) Len() int {
	if b == nil {
		return 0
	}
	return len(b.positions)
}
