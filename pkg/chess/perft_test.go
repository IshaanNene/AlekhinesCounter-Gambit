package chess

import "testing"

// perft counts the number of leaf nodes in the move tree to the given depth.
// It is the standard correctness test for a chess move generator.
func perft(b *Board, depth int) int64 {
	if depth == 0 {
		return 1
	}
	var nodes int64
	for _, m := range b.LegalMoves() {
		nb := b.applyMoveUnchecked(m)
		nodes += perft(nb, depth-1)
	}
	return nodes
}

func TestPerft(t *testing.T) {
	cases := []struct {
		name  string
		fen   string
		depth int
		want  int64
	}{
		// Standard starting position — canonical perft values.
		{"start-d1", StartFEN, 1, 20},
		{"start-d2", StartFEN, 2, 400},
		{"start-d3", StartFEN, 3, 8902},
		{"start-d4", StartFEN, 4, 197281},
		// Kiwipete: dense middlegame exercising castling, pins, en passant.
		{"kiwipete-d1", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 1, 48},
		{"kiwipete-d2", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 2, 2039},
		{"kiwipete-d3", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 3, 97862},
		// Position 3: promotions, en passant, and rook endgame tactics.
		{"pos3-d1", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 1, 14},
		{"pos3-d2", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 2, 191},
		{"pos3-d3", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 3, 2812},
		{"pos3-d4", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 4, 43238},
		// Position 4: heavy promotion / underpromotion tree.
		{"pos4-d1", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 1, 6},
		{"pos4-d2", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 2, 264},
		{"pos4-d3", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 3, 9467},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ParseFEN(tc.fen)
			if err != nil {
				t.Fatalf("ParseFEN(%q): %v", tc.fen, err)
			}
			got := perft(b, tc.depth)
			if got != tc.want {
				t.Errorf("perft(%q, %d) = %d, want %d", tc.fen, tc.depth, got, tc.want)
			}
		})
	}
}
