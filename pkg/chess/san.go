package chess

import (
	"fmt"
	"strings"
)

// SAN renders a move in Standard Algebraic Notation ("Nf3", "exd5", "O-O",
// "Qxf7#") as played in the given position.
//
// SAN is contextual: "Nf3" is only unambiguous if one knight can reach f3, and
// the check/mate suffix depends on the resulting position. So this needs the
// board the move is played from, which is why it lives here rather than on Move.
func (b *Board) SAN(m Move) string {
	piece := b.squares[m.From]
	if piece.IsEmpty() {
		return m.String() // not a real move from here; fall back to UCI
	}

	// Castling is written by king destination file, not the pieces moved.
	if piece.Type() == King && abs(fileOf(m.To)-fileOf(m.From)) == 2 {
		san := "O-O"
		if fileOf(m.To) == 2 {
			san = "O-O-O"
		}
		return san + b.checkSuffix(m)
	}

	var sb strings.Builder
	isCapture := !b.squares[m.To].IsEmpty() ||
		(piece.Type() == Pawn && m.To == b.EnPassant)

	if piece.Type() == Pawn {
		if isCapture {
			// A pawn capture names its origin file: "exd5".
			sb.WriteByte(byte('a' + fileOf(m.From)))
			sb.WriteByte('x')
		}
		sb.WriteString(squareName(m.To))
		if m.Promotion != NoPieceType {
			sb.WriteByte('=')
			sb.WriteByte(pieceLetterUpper(m.Promotion))
		}
	} else {
		sb.WriteByte(pieceLetterUpper(piece.Type()))
		sb.WriteString(b.disambiguation(m, piece))
		if isCapture {
			sb.WriteByte('x')
		}
		sb.WriteString(squareName(m.To))
	}

	return sb.String() + b.checkSuffix(m)
}

// disambiguation returns the file, rank, or both needed to tell this move apart
// from another same-type piece that could also reach the destination.
//
// The rule is specific: prefer file, then rank, then both — and only add what is
// actually needed. "Nbd2" when two knights can reach d2 from different files;
// "N1d2" when they share a file; "Qh4e1" only when neither alone suffices.
func (b *Board) disambiguation(m Move, piece Piece) string {
	var rivals []int
	for _, lm := range b.LegalMoves() {
		if lm.To != m.To || lm.From == m.From {
			continue
		}
		if b.squares[lm.From] == piece {
			rivals = append(rivals, lm.From)
		}
	}
	if len(rivals) == 0 {
		return ""
	}

	sameFile, sameRank := false, false
	for _, r := range rivals {
		if fileOf(r) == fileOf(m.From) {
			sameFile = true
		}
		if rankOf(r) == rankOf(m.From) {
			sameRank = true
		}
	}
	switch {
	case !sameFile:
		return string(rune('a' + fileOf(m.From)))
	case !sameRank:
		return string(rune('1' + rankOf(m.From)))
	default:
		return squareName(m.From)
	}
}

// checkSuffix returns "+" for check, "#" for checkmate, or "".
func (b *Board) checkSuffix(m Move) string {
	after := b.applyMoveUnchecked(m)
	if !after.InCheck() {
		return ""
	}
	if len(after.LegalMoves()) == 0 {
		return "#"
	}
	return "+"
}

func pieceLetterUpper(t PieceType) byte {
	switch t {
	case Knight:
		return 'N'
	case Bishop:
		return 'B'
	case Rook:
		return 'R'
	case Queen:
		return 'Q'
	case King:
		return 'K'
	default:
		return 'P'
	}
}

// PGN renders a completed game as a PGN document.
//
// startFEN is the initial position; ucis are the moves in order; tags are the
// PGN seven-tag roster plus any extras. Moves are converted to SAN by replaying
// them, so the output is a normal PGN any chess program can open — not a UCI
// dump dressed up as one.
func PGN(startFEN string, ucis []string, tags map[string]string) (string, error) {
	board, err := ParseFEN(startFEN)
	if err != nil {
		return "", fmt.Errorf("parse start fen: %w", err)
	}

	var sb strings.Builder
	// The seven-tag roster in its required order, then any extras.
	for _, k := range []string{"Event", "Site", "Date", "Round", "White", "Black", "Result"} {
		v := tags[k]
		if v == "" {
			v = "?"
		}
		fmt.Fprintf(&sb, "[%s %q]\n", k, v)
	}
	for k, v := range tags {
		switch k {
		case "Event", "Site", "Date", "Round", "White", "Black", "Result":
			// already written
		default:
			fmt.Fprintf(&sb, "[%s %q]\n", k, v)
		}
	}
	sb.WriteByte('\n')

	// Move text. Full-move numbers come from the board so a game that starts from
	// a non-standard position (Black to move, high move count) is still correct.
	var movetext strings.Builder
	for _, uci := range ucis {
		m, err := ParseUCIMove(uci)
		if err != nil {
			return "", fmt.Errorf("parse move %q: %w", uci, err)
		}
		if board.Turn == White {
			fmt.Fprintf(&movetext, "%d. ", board.FullMove)
		} else if movetext.Len() == 0 {
			// A game beginning with Black to move needs the "N..." marker.
			fmt.Fprintf(&movetext, "%d... ", board.FullMove)
		}
		movetext.WriteString(board.SAN(m))
		movetext.WriteByte(' ')

		next, err := board.ApplyMove(m)
		if err != nil {
			return "", fmt.Errorf("illegal move %q in game: %w", uci, err)
		}
		board = next
	}

	result := tags["Result"]
	if result == "" {
		result = "*"
	}
	sb.WriteString(wrapMovetext(strings.TrimSpace(movetext.String())+" "+result, 80))
	sb.WriteByte('\n')
	return sb.String(), nil
}

// wrapMovetext wraps at a column boundary without splitting a token, as PGN
// readers expect lines under ~80 characters.
func wrapMovetext(s string, width int) string {
	words := strings.Fields(s)
	var out strings.Builder
	lineLen := 0
	for i, w := range words {
		if lineLen > 0 && lineLen+1+len(w) > width {
			out.WriteByte('\n')
			lineLen = 0
		} else if i > 0 {
			out.WriteByte(' ')
			lineLen++
		}
		out.WriteString(w)
		lineLen += len(w)
	}
	return out.String()
}
