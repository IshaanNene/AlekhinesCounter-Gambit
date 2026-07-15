package chess

import (
	"errors"
	"fmt"
)

// ErrIllegalMove indicates a syntactically valid move that is not legal in the
// current position.
var ErrIllegalMove = errors.New("illegal move")

// LegalMoves returns every legal move for the side to move.
func (b *Board) LegalMoves() []Move {
	pseudo := b.pseudoLegalMoves()
	legal := make([]Move, 0, len(pseudo))
	for _, m := range pseudo {
		nb := b.applyMoveUnchecked(m)
		// After the move it is the opponent's turn; ensure the mover's king is safe.
		if !nb.IsAttacked(nb.kingSquare(b.Turn), b.Turn.Opposite()) {
			legal = append(legal, m)
		}
	}
	return legal
}

// IsLegal reports whether m is legal in the current position.
func (b *Board) IsLegal(m Move) bool {
	for _, lm := range b.LegalMoves() {
		if lm == m {
			return true
		}
	}
	return false
}

// ApplyMove validates m and returns the resulting position. The receiver is not
// modified. It returns ErrIllegalMove if the move is not legal.
func (b *Board) ApplyMove(m Move) (*Board, error) {
	if !b.IsLegal(m) {
		return nil, fmt.Errorf("%w: %s", ErrIllegalMove, m)
	}
	return b.applyMoveUnchecked(m), nil
}

// ApplyUCI parses and applies a UCI move.
func (b *Board) ApplyUCI(uci string) (*Board, error) {
	m, err := ParseUCIMove(uci)
	if err != nil {
		return nil, err
	}
	return b.ApplyMove(m)
}

// pseudoLegalMoves generates moves that follow piece movement rules but may
// leave the mover's own king in check (filtered out by LegalMoves).
func (b *Board) pseudoLegalMoves() []Move {
	moves := make([]Move, 0, 48)
	us := b.Turn
	for s := 0; s < 64; s++ {
		p := b.squares[s]
		if p.IsEmpty() || p.Color() != us {
			continue
		}
		switch p.Type() {
		case Pawn:
			moves = b.genPawn(moves, s, us)
		case Knight:
			moves = b.genStep(moves, s, us, knightDeltas[:])
		case King:
			moves = b.genStep(moves, s, us, kingDeltas[:])
			moves = b.genCastling(moves, us)
		case Bishop:
			moves = b.genSliding(moves, s, us, bishopDirs[:])
		case Rook:
			moves = b.genSliding(moves, s, us, rookDirs[:])
		case Queen:
			moves = b.genSliding(moves, s, us, bishopDirs[:])
			moves = b.genSliding(moves, s, us, rookDirs[:])
		}
	}
	return moves
}

// genStep generates non-sliding moves (knight, king) from square s.
func (b *Board) genStep(moves []Move, s int, us Color, deltas [][2]int) []Move {
	f, r := fileOf(s), rankOf(s)
	for _, d := range deltas {
		nf, nr := f+d[0], r+d[1]
		if !onBoard(nf, nr) {
			continue
		}
		t := sq(nf, nr)
		dst := b.squares[t]
		if dst.IsEmpty() || dst.Color() != us {
			moves = append(moves, Move{From: s, To: t})
		}
	}
	return moves
}

// genSliding generates sliding moves (bishop, rook, queen) from square s.
func (b *Board) genSliding(moves []Move, s int, us Color, dirs [][2]int) []Move {
	f, r := fileOf(s), rankOf(s)
	for _, d := range dirs {
		nf, nr := f+d[0], r+d[1]
		for onBoard(nf, nr) {
			t := sq(nf, nr)
			dst := b.squares[t]
			if dst.IsEmpty() {
				moves = append(moves, Move{From: s, To: t})
			} else {
				if dst.Color() != us {
					moves = append(moves, Move{From: s, To: t})
				}
				break
			}
			nf += d[0]
			nr += d[1]
		}
	}
	return moves
}

// genPawn generates pawn pushes, captures, en passant, and promotions.
func (b *Board) genPawn(moves []Move, s int, us Color) []Move {
	f, r := fileOf(s), rankOf(s)
	var dir, startRank, promoRank int
	if us == White {
		dir, startRank, promoRank = 1, 1, 7
	} else {
		dir, startRank, promoRank = -1, 6, 0
	}

	// Single push.
	oneRank := r + dir
	if oneRank >= 0 && oneRank < 8 {
		one := sq(f, oneRank)
		if b.squares[one].IsEmpty() {
			moves = b.addPawnMove(moves, s, one, oneRank == promoRank)
			// Double push from the starting rank.
			if r == startRank {
				twoRank := r + 2*dir
				two := sq(f, twoRank)
				if b.squares[two].IsEmpty() {
					moves = append(moves, Move{From: s, To: two})
				}
			}
		}
	}

	// Captures (including en passant).
	for _, df := range [2]int{-1, 1} {
		nf := f + df
		nr := r + dir
		if !onBoard(nf, nr) {
			continue
		}
		t := sq(nf, nr)
		dst := b.squares[t]
		if !dst.IsEmpty() && dst.Color() != us {
			moves = b.addPawnMove(moves, s, t, nr == promoRank)
		} else if t == b.EnPassant && dst.IsEmpty() {
			moves = append(moves, Move{From: s, To: t})
		}
	}
	return moves
}

// addPawnMove appends a pawn move, expanding into the four promotions when the
// destination is the promotion rank.
func (b *Board) addPawnMove(moves []Move, from, to int, promo bool) []Move {
	if !promo {
		return append(moves, Move{From: from, To: to})
	}
	for _, pt := range [4]PieceType{Queen, Rook, Bishop, Knight} {
		moves = append(moves, Move{From: from, To: to, Promotion: pt})
	}
	return moves
}

// genCastling generates castling moves (without the king-safety filter, which
// LegalMoves applies; the through/into-check squares are checked here).
func (b *Board) genCastling(moves []Move, us Color) []Move {
	enemy := us.Opposite()
	if us == White {
		// King must be on e1 and not currently in check.
		if b.squares[sq(4, 0)] != MakePiece(White, King) || b.IsAttacked(sq(4, 0), enemy) {
			return moves
		}
		// Kingside: f1,g1 empty; f1,g1 not attacked; right present; rook on h1.
		if b.Castling&CastleWhiteKing != 0 &&
			b.squares[sq(5, 0)].IsEmpty() && b.squares[sq(6, 0)].IsEmpty() &&
			!b.IsAttacked(sq(5, 0), enemy) && !b.IsAttacked(sq(6, 0), enemy) &&
			b.squares[sq(7, 0)] == MakePiece(White, Rook) {
			moves = append(moves, Move{From: sq(4, 0), To: sq(6, 0)})
		}
		// Queenside: b1,c1,d1 empty; c1,d1 not attacked; right present; rook on a1.
		if b.Castling&CastleWhiteQueen != 0 &&
			b.squares[sq(1, 0)].IsEmpty() && b.squares[sq(2, 0)].IsEmpty() && b.squares[sq(3, 0)].IsEmpty() &&
			!b.IsAttacked(sq(2, 0), enemy) && !b.IsAttacked(sq(3, 0), enemy) &&
			b.squares[sq(0, 0)] == MakePiece(White, Rook) {
			moves = append(moves, Move{From: sq(4, 0), To: sq(2, 0)})
		}
	} else {
		if b.squares[sq(4, 7)] != MakePiece(Black, King) || b.IsAttacked(sq(4, 7), enemy) {
			return moves
		}
		if b.Castling&CastleBlackKing != 0 &&
			b.squares[sq(5, 7)].IsEmpty() && b.squares[sq(6, 7)].IsEmpty() &&
			!b.IsAttacked(sq(5, 7), enemy) && !b.IsAttacked(sq(6, 7), enemy) &&
			b.squares[sq(7, 7)] == MakePiece(Black, Rook) {
			moves = append(moves, Move{From: sq(4, 7), To: sq(6, 7)})
		}
		if b.Castling&CastleBlackQueen != 0 &&
			b.squares[sq(1, 7)].IsEmpty() && b.squares[sq(2, 7)].IsEmpty() && b.squares[sq(3, 7)].IsEmpty() &&
			!b.IsAttacked(sq(2, 7), enemy) && !b.IsAttacked(sq(3, 7), enemy) &&
			b.squares[sq(0, 7)] == MakePiece(Black, Rook) {
			moves = append(moves, Move{From: sq(4, 7), To: sq(2, 7)})
		}
	}
	return moves
}

// applyMoveUnchecked applies m assuming it is pseudo-legal, returning a new board.
func (b *Board) applyMoveUnchecked(m Move) *Board {
	nb := b.Clone()
	us := b.Turn
	moving := nb.squares[m.From]
	captured := nb.squares[m.To]
	isPawn := moving.Type() == Pawn

	// Move the piece.
	nb.squares[m.To] = moving
	nb.squares[m.From] = Empty

	// En-passant capture: the captured pawn is behind the destination square.
	if isPawn && m.To == b.EnPassant && captured.IsEmpty() {
		if us == White {
			nb.squares[m.To-8] = Empty
		} else {
			nb.squares[m.To+8] = Empty
		}
	}

	// Promotion.
	if m.Promotion != NoPieceType {
		nb.squares[m.To] = MakePiece(us, m.Promotion)
	}

	// Castling: move the rook alongside the king.
	if moving.Type() == King && abs(fileOf(m.To)-fileOf(m.From)) == 2 {
		rank := rankOf(m.From)
		if fileOf(m.To) == 6 { // kingside
			nb.squares[sq(5, rank)] = nb.squares[sq(7, rank)]
			nb.squares[sq(7, rank)] = Empty
		} else { // queenside
			nb.squares[sq(3, rank)] = nb.squares[sq(0, rank)]
			nb.squares[sq(0, rank)] = Empty
		}
	}

	// Update castling rights.
	nb.updateCastlingRights(m, moving, captured)

	// Set the en-passant target on a double pawn push, else clear it.
	nb.EnPassant = NoSquare
	if isPawn && abs(rankOf(m.To)-rankOf(m.From)) == 2 {
		nb.EnPassant = (m.From + m.To) / 2
	}

	// Half-move clock: reset on pawn move or capture, else increment.
	if isPawn || !captured.IsEmpty() {
		nb.HalfMove = 0
	} else {
		nb.HalfMove = b.HalfMove + 1
	}

	// Full-move number increments after Black moves.
	if us == Black {
		nb.FullMove = b.FullMove + 1
	}

	nb.Turn = us.Opposite()
	return nb
}

// updateCastlingRights clears rights when kings/rooks move or rooks are captured.
func (b *Board) updateCastlingRights(m Move, moving, captured Piece) {
	switch {
	case moving.Type() == King && moving.Color() == White:
		b.Castling &^= CastleWhiteKing | CastleWhiteQueen
	case moving.Type() == King && moving.Color() == Black:
		b.Castling &^= CastleBlackKing | CastleBlackQueen
	}
	// A rook leaving its home square forfeits that side's right.
	clearForSquare := func(s int) {
		switch s {
		case sq(0, 0):
			b.Castling &^= CastleWhiteQueen
		case sq(7, 0):
			b.Castling &^= CastleWhiteKing
		case sq(0, 7):
			b.Castling &^= CastleBlackQueen
		case sq(7, 7):
			b.Castling &^= CastleBlackKing
		}
	}
	clearForSquare(m.From) // rook moved away
	clearForSquare(m.To)   // rook captured on its home square
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
