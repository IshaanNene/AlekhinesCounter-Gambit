package chess

import "testing"

func TestFENRoundTrip(t *testing.T) {
	fens := []string{
		StartFEN,
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"rnbqkbnr/pp1ppppp/8/2p5/4P3/8/PPPP1PPP/RNBQKBNR w KQkq c6 0 2",
		"4k3/8/8/8/8/8/8/4K3 b - - 12 34",
		"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1",
	}
	for _, fen := range fens {
		b, err := ParseFEN(fen)
		if err != nil {
			t.Fatalf("ParseFEN(%q): %v", fen, err)
		}
		if got := b.FEN(); got != fen {
			t.Errorf("FEN round-trip: got %q, want %q", got, fen)
		}
	}
}

func TestParseFENErrors(t *testing.T) {
	bad := []string{
		"",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq -", // missing fields
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP w KQkq - 0 1",      // 7 ranks
		"xnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR x KQkq - 0 1",
	}
	for _, fen := range bad {
		if _, err := ParseFEN(fen); err == nil {
			t.Errorf("ParseFEN(%q): expected error, got nil", fen)
		}
	}
}

func TestCheckmate(t *testing.T) {
	// Fool's mate: 1. f3 e5 2. g4 Qh4#
	b := NewBoard()
	for _, uci := range []string{"f2f3", "e7e5", "g2g4", "d8h4"} {
		nb, err := b.ApplyUCI(uci)
		if err != nil {
			t.Fatalf("ApplyUCI(%q): %v", uci, err)
		}
		b = nb
	}
	res, reason := b.Outcome()
	if res != BlackWins || reason != Checkmate {
		t.Errorf("Outcome() = (%v, %v), want (BlackWins, Checkmate)", res, reason)
	}
}

func TestStalemate(t *testing.T) {
	// Classic king+queen stalemate: Black to move, no legal moves, not in check.
	b, err := ParseFEN("7k/5Q2/6K1/8/8/8/8/8 b - - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	res, reason := b.Outcome()
	if res != Draw || reason != Stalemate {
		t.Errorf("Outcome() = (%v, %v), want (Draw, Stalemate)", res, reason)
	}
}

func TestInsufficientMaterial(t *testing.T) {
	draws := []string{
		"4k3/8/8/8/8/8/8/4K3 w - - 0 1",    // K vs K
		"4k3/8/8/8/8/8/8/4KN2 w - - 0 1",   // K vs KN
		"4k3/8/8/8/8/8/8/4KB2 w - - 0 1",   // K vs KB
		"2b1k3/8/8/8/8/8/8/4KB2 w - - 0 1", // KB vs KB, both bishops on dark squares
	}
	for _, fen := range draws {
		b, _ := ParseFEN(fen)
		if !b.InsufficientMaterial() {
			t.Errorf("InsufficientMaterial(%q) = false, want true", fen)
		}
	}
	sufficient := []string{
		StartFEN,
		"4k3/8/8/8/8/8/8/4KR2 w - - 0 1",  // rook mates
		"4k3/8/8/8/8/8/4P3/4K3 w - - 0 1", // pawn can promote
	}
	for _, fen := range sufficient {
		b, _ := ParseFEN(fen)
		if b.InsufficientMaterial() {
			t.Errorf("InsufficientMaterial(%q) = true, want false", fen)
		}
	}
}

func TestIllegalMoveRejected(t *testing.T) {
	b := NewBoard()
	// Moving into a self-pin / illegal jump: e2 to e5 (pawn can't jump three).
	if _, err := b.ApplyUCI("e2e5"); err == nil {
		t.Error("expected e2e5 to be illegal from the start position")
	}
	// A legal move should succeed.
	if _, err := b.ApplyUCI("e2e4"); err != nil {
		t.Errorf("e2e4 should be legal: %v", err)
	}
}

func TestEnPassant(t *testing.T) {
	// After 1. e4 the en-passant square is unset; set up a real en passant.
	b, err := ParseFEN("rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3")
	if err != nil {
		t.Fatal(err)
	}
	nb, err := b.ApplyUCI("e5f6") // en passant capture
	if err != nil {
		t.Fatalf("en passant e5f6: %v", err)
	}
	// The captured pawn on f5 must be gone and f6 occupied by the white pawn.
	if !nb.PieceAt(sq(5, 4)).IsEmpty() {
		t.Error("captured pawn on f5 was not removed")
	}
	if nb.PieceAt(sq(5, 5)) != MakePiece(White, Pawn) {
		t.Error("white pawn not on f6 after en passant")
	}
}
