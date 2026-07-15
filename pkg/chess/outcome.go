package chess

// Result is the outcome of a position.
type Result uint8

const (
	// InProgress means the game is not over.
	InProgress Result = iota
	WhiteWins
	BlackWins
	Draw
)

// EndReason explains why a game ended (Ongoing while in progress).
type EndReason uint8

const (
	Ongoing EndReason = iota
	Checkmate
	Stalemate
	InsufficientMaterial
	FiftyMove
	ThreefoldRepetition
)

// Outcome reports the result and reason for the current position, considering
// checkmate, stalemate, insufficient material, and the fifty-move rule.
// Threefold repetition depends on move history and is handled by OutcomeWithHistory.
func (b *Board) Outcome() (Result, EndReason) {
	if len(b.LegalMoves()) == 0 {
		if b.InCheck() {
			if b.Turn == White {
				return BlackWins, Checkmate // White is mated
			}
			return WhiteWins, Checkmate
		}
		return Draw, Stalemate
	}
	if b.InsufficientMaterial() {
		return Draw, InsufficientMaterial
	}
	if b.HalfMove >= 100 { // 50 full moves by each side
		return Draw, FiftyMove
	}
	return InProgress, Ongoing
}

// OutcomeWithHistory is like Outcome but also declares a draw when the current
// position (by FEN, ignoring clocks) has occurred at least three times in the
// supplied history. History should contain the FENs of all positions reached,
// including the current one.
func (b *Board) OutcomeWithHistory(fenHistory []string) (Result, EndReason) {
	if r, reason := b.Outcome(); r != InProgress {
		return r, reason
	}
	if threefold(fenHistory) {
		return Draw, ThreefoldRepetition
	}
	return InProgress, Ongoing
}

// threefold reports whether any position (matched on the first four FEN fields:
// placement, side, castling, en passant) appears at least three times.
func threefold(fenHistory []string) bool {
	counts := make(map[string]int, len(fenHistory))
	for _, f := range fenHistory {
		counts[repetitionKey(f)]++
		if counts[repetitionKey(f)] >= 3 {
			return true
		}
	}
	return false
}

// repetitionKey strips the half-move and full-move counters from a FEN so that
// only the position-relevant fields are compared.
func repetitionKey(fen string) string {
	fields := 0
	for i := 0; i < len(fen); i++ {
		if fen[i] == ' ' {
			fields++
			if fields == 4 {
				return fen[:i]
			}
		}
	}
	return fen
}

// InsufficientMaterial reports whether neither side has enough material to mate:
// K vs K, K vs KN, K vs KB, and KB vs KB with bishops on the same color.
func (b *Board) InsufficientMaterial() bool {
	var whiteMinor, blackMinor int
	bishopSquares := make([]int, 0, 2)
	for s := 0; s < 64; s++ {
		p := b.squares[s]
		if p.IsEmpty() {
			continue
		}
		switch p.Type() {
		case King:
			// ignore
		case Bishop:
			bishopSquares = append(bishopSquares, s)
			if p.Color() == White {
				whiteMinor++
			} else {
				blackMinor++
			}
		case Knight:
			if p.Color() == White {
				whiteMinor++
			} else {
				blackMinor++
			}
		default:
			// Any pawn, rook, or queen means mating material exists.
			return false
		}
	}

	total := whiteMinor + blackMinor
	switch {
	case total == 0: // K vs K
		return true
	case total == 1: // K vs KN or K vs KB
		return true
	case total == 2 && len(bishopSquares) == 2:
		// KB vs KB is a draw only when both bishops are on the same color square.
		return squareColor(bishopSquares[0]) == squareColor(bishopSquares[1])
	default:
		return false
	}
}

// squareColor returns 0 for light squares and 1 for dark squares.
func squareColor(s int) int { return (fileOf(s) + rankOf(s)) % 2 }
