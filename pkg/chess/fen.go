package chess

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// StartFEN is the standard chess starting position.
const StartFEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

// ErrInvalidFEN indicates a malformed FEN string.
var ErrInvalidFEN = errors.New("invalid FEN")

// fenPieceToPiece maps FEN piece letters to pieces.
var fenPieceToPiece = map[byte]Piece{
	'P': MakePiece(White, Pawn), 'N': MakePiece(White, Knight), 'B': MakePiece(White, Bishop),
	'R': MakePiece(White, Rook), 'Q': MakePiece(White, Queen), 'K': MakePiece(White, King),
	'p': MakePiece(Black, Pawn), 'n': MakePiece(Black, Knight), 'b': MakePiece(Black, Bishop),
	'r': MakePiece(Black, Rook), 'q': MakePiece(Black, Queen), 'k': MakePiece(Black, King),
}

// pieceToFEN maps pieces back to FEN letters.
var pieceToFEN = map[Piece]byte{
	MakePiece(White, Pawn): 'P', MakePiece(White, Knight): 'N', MakePiece(White, Bishop): 'B',
	MakePiece(White, Rook): 'R', MakePiece(White, Queen): 'Q', MakePiece(White, King): 'K',
	MakePiece(Black, Pawn): 'p', MakePiece(Black, Knight): 'n', MakePiece(Black, Bishop): 'b',
	MakePiece(Black, Rook): 'r', MakePiece(Black, Queen): 'q', MakePiece(Black, King): 'k',
}

// NewBoard returns the standard starting position.
func NewBoard() *Board {
	b, err := ParseFEN(StartFEN)
	if err != nil {
		panic("chess: StartFEN failed to parse: " + err.Error())
	}
	return b
}

// ParseFEN parses a FEN string into a Board.
func ParseFEN(fen string) (*Board, error) {
	fields := strings.Fields(fen)
	if len(fields) != 6 {
		return nil, fmt.Errorf("%w: expected 6 fields, got %d", ErrInvalidFEN, len(fields))
	}

	b := &Board{EnPassant: NoSquare}

	// Field 1: piece placement, ranks 8..1 separated by '/'.
	ranks := strings.Split(fields[0], "/")
	if len(ranks) != 8 {
		return nil, fmt.Errorf("%w: expected 8 ranks, got %d", ErrInvalidFEN, len(ranks))
	}
	for i, rankStr := range ranks {
		rank := 7 - i // FEN lists rank 8 first
		file := 0
		for j := 0; j < len(rankStr); j++ {
			ch := rankStr[j]
			if ch >= '1' && ch <= '8' {
				file += int(ch - '0')
				continue
			}
			p, ok := fenPieceToPiece[ch]
			if !ok {
				return nil, fmt.Errorf("%w: bad piece %q", ErrInvalidFEN, string(ch))
			}
			if file > 7 {
				return nil, fmt.Errorf("%w: rank %d overflows", ErrInvalidFEN, rank+1)
			}
			b.squares[sq(file, rank)] = p
			file++
		}
		if file != 8 {
			return nil, fmt.Errorf("%w: rank %d has %d files", ErrInvalidFEN, rank+1, file)
		}
	}

	// Field 2: side to move.
	switch fields[1] {
	case "w":
		b.Turn = White
	case "b":
		b.Turn = Black
	default:
		return nil, fmt.Errorf("%w: bad side to move %q", ErrInvalidFEN, fields[1])
	}

	// Field 3: castling rights.
	if fields[2] != "-" {
		for i := 0; i < len(fields[2]); i++ {
			switch fields[2][i] {
			case 'K':
				b.Castling |= CastleWhiteKing
			case 'Q':
				b.Castling |= CastleWhiteQueen
			case 'k':
				b.Castling |= CastleBlackKing
			case 'q':
				b.Castling |= CastleBlackQueen
			default:
				return nil, fmt.Errorf("%w: bad castling %q", ErrInvalidFEN, fields[2])
			}
		}
	}

	// Field 4: en-passant target square.
	if fields[3] != "-" {
		if len(fields[3]) != 2 {
			return nil, fmt.Errorf("%w: bad en passant %q", ErrInvalidFEN, fields[3])
		}
		s, ok := parseSquare(fields[3][0], fields[3][1])
		if !ok {
			return nil, fmt.Errorf("%w: bad en passant %q", ErrInvalidFEN, fields[3])
		}
		b.EnPassant = s
	}

	// Fields 5 and 6: half-move clock and full-move number.
	hm, err := strconv.Atoi(fields[4])
	if err != nil || hm < 0 {
		return nil, fmt.Errorf("%w: bad halfmove clock %q", ErrInvalidFEN, fields[4])
	}
	b.HalfMove = hm
	fm, err := strconv.Atoi(fields[5])
	if err != nil || fm < 1 {
		return nil, fmt.Errorf("%w: bad fullmove number %q", ErrInvalidFEN, fields[5])
	}
	b.FullMove = fm

	return b, nil
}

// FEN renders the board as a FEN string.
func (b *Board) FEN() string {
	var sb strings.Builder

	// Piece placement, rank 8 down to rank 1.
	for rank := 7; rank >= 0; rank-- {
		empty := 0
		for file := 0; file < 8; file++ {
			p := b.squares[sq(file, rank)]
			if p.IsEmpty() {
				empty++
				continue
			}
			if empty > 0 {
				sb.WriteByte(byte('0' + empty))
				empty = 0
			}
			sb.WriteByte(pieceToFEN[p])
		}
		if empty > 0 {
			sb.WriteByte(byte('0' + empty))
		}
		if rank > 0 {
			sb.WriteByte('/')
		}
	}

	// Side to move.
	if b.Turn == White {
		sb.WriteString(" w ")
	} else {
		sb.WriteString(" b ")
	}

	// Castling rights.
	if b.Castling == 0 {
		sb.WriteByte('-')
	} else {
		if b.Castling&CastleWhiteKing != 0 {
			sb.WriteByte('K')
		}
		if b.Castling&CastleWhiteQueen != 0 {
			sb.WriteByte('Q')
		}
		if b.Castling&CastleBlackKing != 0 {
			sb.WriteByte('k')
		}
		if b.Castling&CastleBlackQueen != 0 {
			sb.WriteByte('q')
		}
	}

	// En passant.
	sb.WriteByte(' ')
	if b.EnPassant == NoSquare {
		sb.WriteByte('-')
	} else {
		sb.WriteString(squareName(b.EnPassant))
	}

	// Clocks.
	sb.WriteByte(' ')
	sb.WriteString(strconv.Itoa(b.HalfMove))
	sb.WriteByte(' ')
	sb.WriteString(strconv.Itoa(b.FullMove))

	return sb.String()
}
