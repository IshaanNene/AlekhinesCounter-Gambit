package chess

import (
	"errors"
	"fmt"
)

// Move is a single move: origin and destination squares plus an optional
// promotion piece type (NoPieceType when the move is not a promotion).
type Move struct {
	From      int
	To        int
	Promotion PieceType
}

// ErrInvalidMove indicates a syntactically malformed UCI move string.
var ErrInvalidMove = errors.New("invalid move")

// promoLetters maps promotion piece types to their UCI letters and back.
var promoToLetter = map[PieceType]byte{Knight: 'n', Bishop: 'b', Rook: 'r', Queen: 'q'}
var letterToPromo = map[byte]PieceType{'n': Knight, 'b': Bishop, 'r': Rook, 'q': Queen}

// ParseUCIMove parses a move in UCI long algebraic notation ("e2e4", "e7e8q").
// It validates only the syntax and square ranges, not legality.
func ParseUCIMove(s string) (Move, error) {
	if len(s) != 4 && len(s) != 5 {
		return Move{}, fmt.Errorf("%w: %q", ErrInvalidMove, s)
	}
	from, ok := parseSquare(s[0], s[1])
	if !ok {
		return Move{}, fmt.Errorf("%w: bad from-square in %q", ErrInvalidMove, s)
	}
	to, ok := parseSquare(s[2], s[3])
	if !ok {
		return Move{}, fmt.Errorf("%w: bad to-square in %q", ErrInvalidMove, s)
	}
	m := Move{From: from, To: to}
	if len(s) == 5 {
		pt, ok := letterToPromo[s[4]]
		if !ok {
			return Move{}, fmt.Errorf("%w: bad promotion in %q", ErrInvalidMove, s)
		}
		m.Promotion = pt
	}
	return m, nil
}

// String renders the move in UCI notation.
func (m Move) String() string {
	s := squareName(m.From) + squareName(m.To)
	if m.Promotion != NoPieceType {
		s += string(promoToLetter[m.Promotion])
	}
	return s
}

// parseSquare parses file/rank bytes ('a'-'h', '1'-'8') into a square index.
func parseSquare(fileByte, rankByte byte) (int, bool) {
	if fileByte < 'a' || fileByte > 'h' || rankByte < '1' || rankByte > '8' {
		return 0, false
	}
	return sq(int(fileByte-'a'), int(rankByte-'1')), true
}

// squareName returns the algebraic name of a square (e.g. "e4").
func squareName(s int) string {
	return string(rune('a'+fileOf(s))) + string(rune('1'+rankOf(s)))
}
