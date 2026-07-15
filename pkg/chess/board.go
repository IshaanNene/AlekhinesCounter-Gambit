// Package chess implements chess rules: board representation, FEN
// (de)serialization, legal move generation, and game-terminal detection.
//
// The package has no external dependencies and performs no I/O. Squares are
// indexed 0..63 with a1=0, h1=7, a8=56, h8=63 (square = rank*8 + file, where
// rank 0 is White's back rank and file 0 is the a-file). Moves are expressed in
// UCI long algebraic notation (e.g. "e2e4", "e7e8q").
package chess

// Color is the side of a piece or the side to move.
type Color uint8

const (
	White Color = iota
	Black
)

// Opposite returns the other color.
func (c Color) Opposite() Color { return c ^ 1 }

func (c Color) String() string {
	if c == White {
		return "white"
	}
	return "black"
}

// PieceType identifies a kind of piece. NoPieceType is the zero value.
type PieceType uint8

const (
	NoPieceType PieceType = iota
	Pawn
	Knight
	Bishop
	Rook
	Queen
	King
)

// Piece is a colored piece packed into a byte: the low 3 bits hold the
// PieceType (1..6) and bit 3 holds the Color. The zero value is the empty square.
type Piece uint8

// Empty is the absence of a piece.
const Empty Piece = 0

// MakePiece builds a Piece from a color and type.
func MakePiece(c Color, t PieceType) Piece { return Piece(uint8(t) | uint8(c)<<3) }

// Type returns the piece's type.
func (p Piece) Type() PieceType { return PieceType(p & 0x7) }

// Color returns the piece's color (meaningless for Empty).
func (p Piece) Color() Color { return Color((p >> 3) & 1) }

// IsEmpty reports whether the square holds no piece.
func (p Piece) IsEmpty() bool { return p == Empty }

// Castling rights, stored as a bitfield.
const (
	CastleWhiteKing  uint8 = 1 << 0
	CastleWhiteQueen uint8 = 1 << 1
	CastleBlackKing  uint8 = 1 << 2
	CastleBlackQueen uint8 = 1 << 3
)

// NoSquare marks the absence of a square (e.g. no en-passant target).
const NoSquare = -1

// Board is a full chess position.
type Board struct {
	squares   [64]Piece
	Turn      Color
	Castling  uint8
	EnPassant int // target square index, or NoSquare
	HalfMove  int // half-moves since last capture or pawn move (fifty-move rule)
	FullMove  int // starts at 1, incremented after Black moves
}

// Square helpers.
func sq(file, rank int) int { return rank*8 + file }
func fileOf(s int) int      { return s % 8 }
func rankOf(s int) int      { return s / 8 }
func onBoard(file, rank int) bool {
	return file >= 0 && file < 8 && rank >= 0 && rank < 8
}

// PieceAt returns the piece on square s.
func (b *Board) PieceAt(s int) Piece { return b.squares[s] }

// Clone returns a deep copy of the board.
func (b *Board) Clone() *Board {
	c := *b
	return &c
}

// kingSquare returns the square of the given color's king, or NoSquare.
func (b *Board) kingSquare(c Color) int {
	want := MakePiece(c, King)
	for s := 0; s < 64; s++ {
		if b.squares[s] == want {
			return s
		}
	}
	return NoSquare
}

// knightDeltas / kingDeltas as (file, rank) offsets.
var knightDeltas = [8][2]int{
	{1, 2}, {2, 1}, {2, -1}, {1, -2}, {-1, -2}, {-2, -1}, {-2, 1}, {-1, 2},
}
var kingDeltas = [8][2]int{
	{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1},
}
var bishopDirs = [4][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
var rookDirs = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// IsAttacked reports whether square s is attacked by any piece of color `by`.
func (b *Board) IsAttacked(s int, by Color) bool {
	f, r := fileOf(s), rankOf(s)

	// Pawn attacks. A pawn of color `by` attacks diagonally forward; check the
	// two squares from which such a pawn would be attacking s.
	var pawnRank int
	if by == White {
		pawnRank = r - 1 // white pawns sit one rank below the square they attack
	} else {
		pawnRank = r + 1
	}
	if pawnRank >= 0 && pawnRank < 8 {
		for _, df := range [2]int{-1, 1} {
			pf := f + df
			if pf >= 0 && pf < 8 && b.squares[sq(pf, pawnRank)] == MakePiece(by, Pawn) {
				return true
			}
		}
	}

	// Knight attacks.
	for _, d := range knightDeltas {
		nf, nr := f+d[0], r+d[1]
		if onBoard(nf, nr) && b.squares[sq(nf, nr)] == MakePiece(by, Knight) {
			return true
		}
	}

	// King attacks.
	for _, d := range kingDeltas {
		nf, nr := f+d[0], r+d[1]
		if onBoard(nf, nr) && b.squares[sq(nf, nr)] == MakePiece(by, King) {
			return true
		}
	}

	// Sliding attacks: bishops/queens along diagonals, rooks/queens along files/ranks.
	if b.slidingAttack(f, r, bishopDirs[:], by, Bishop) {
		return true
	}
	if b.slidingAttack(f, r, rookDirs[:], by, Rook) {
		return true
	}
	return false
}

// slidingAttack ray-casts from (f,r) along each direction, returning true if the
// first piece encountered is a slider of color `by` of type `slider` or a queen.
func (b *Board) slidingAttack(f, r int, dirs [][2]int, by Color, slider PieceType) bool {
	for _, d := range dirs {
		nf, nr := f+d[0], r+d[1]
		for onBoard(nf, nr) {
			p := b.squares[sq(nf, nr)]
			if !p.IsEmpty() {
				if p.Color() == by && (p.Type() == slider || p.Type() == Queen) {
					return true
				}
				break // blocked by some other piece
			}
			nf += d[0]
			nr += d[1]
		}
	}
	return false
}

// InCheck reports whether the side to move is in check.
func (b *Board) InCheck() bool {
	ks := b.kingSquare(b.Turn)
	if ks == NoSquare {
		return false
	}
	return b.IsAttacked(ks, b.Turn.Opposite())
}
