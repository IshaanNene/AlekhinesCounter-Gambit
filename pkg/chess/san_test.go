package chess

import (
	"strings"
	"testing"
)

func sanOf(t *testing.T, fen, uci string) string {
	t.Helper()
	b, err := ParseFEN(fen)
	if err != nil {
		t.Fatalf("ParseFEN(%q): %v", fen, err)
	}
	m, err := ParseUCIMove(uci)
	if err != nil {
		t.Fatalf("ParseUCIMove(%q): %v", uci, err)
	}
	return b.SAN(m)
}

func TestSANBasics(t *testing.T) {
	cases := []struct {
		name, fen, uci, want string
	}{
		{"pawn push", StartFEN, "e2e4", "e4"},
		{"knight develop", StartFEN, "g1f3", "Nf3"},
		{"pawn capture", "rnbqkbnr/ppp1pppp/8/3p4/4P3/8/PPPP1PPP/RNBQKBNR w KQkq d6 0 2", "e4d5", "exd5"},
		{"piece capture", "rnbqkbnr/ppp1pppp/8/3N4/8/8/PPPP1PPP/R1BQKBNR w KQkq - 0 3", "d5c7", "Nxc7+"},
		{"kingside castle", "rnbqk2r/pppp1ppp/5n2/2b1p3/2B1P3/5N2/PPPP1PPP/RNBQK2R w KQkq - 4 4", "e1g1", "O-O"},
		{"promotion", "8/P7/8/8/8/8/8/k6K w - - 0 1", "a7a8q", "a8=Q+"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanOf(t, tc.fen, tc.uci); got != tc.want {
				t.Errorf("SAN(%s) = %q, want %q", tc.uci, got, tc.want)
			}
		})
	}
}

func TestSANCheckmate(t *testing.T) {
	// Scholar's mate final move: the queen takes f7 with mate.
	fen := "r1bqkbnr/pppp1Qpp/2n5/4p3/2B1P3/8/PPPP1PPP/RNB1K1NR b KQkq - 0 4"
	// That FEN is already mated (Black to move); build the position before Qxf7#.
	before := "r1bqkb1r/pppp1ppp/2n2n2/4p3/2B1P2Q/8/PPPP1PPP/RNB1K1NR w KQkq - 4 4"
	if got := sanOf(t, before, "h4f6"); !strings.HasSuffix(got, "") {
		_ = got // placeholder; real mate checked below
	}
	// Simplest reliable mate: back-rank. White rook to e8 is mate.
	mate := "6k1/5ppp/8/8/8/8/8/4R1K1 w - - 0 1"
	if got := sanOf(t, mate, "e1e8"); got != "Re8#" {
		t.Errorf("back-rank mate = %q, want Re8#", got)
	}
	_ = fen
}

// Two knights that can both reach the same square must be disambiguated, and by
// the minimal amount — file when that suffices, rank only when files collide.
func TestSANDisambiguation(t *testing.T) {
	// Knights on b1 and f3 (via g1) — actually use a crafted position: knights on
	// c3 and g1 can both go to e2? No. Use the classic: knights on d2 and f3 both
	// reach e4? Only from specific squares. Take a clean case: rooks on a1 and h1,
	// both can reach d1.
	// Two rooks on a1 and h1 with a clear first rank both reach d1, so the moving
	// rook's file disambiguates. King is off the back rank so it blocks nothing.
	rooksClear := "7k/8/8/8/8/8/4K3/R6R w - - 0 1"
	if got := sanOf(t, rooksClear, "a1d1"); !strings.HasPrefix(got, "Rad1") {
		t.Errorf("rook disambiguation = %q, want Rad1 prefix", got)
	}
	if got := sanOf(t, rooksClear, "h1d1"); !strings.HasPrefix(got, "Rhd1") {
		t.Errorf("rook disambiguation = %q, want Rhd1 prefix", got)
	}

	// Two knights sharing a file (b1 and b5) both reaching d4? b1->d2, no.
	// Knights on c2 and c6 both reach e3? c6->e3 no. Use a1 and a5 -> both reach
	// b3: a1b3 and a5b3. They share the a-file, so rank is needed.
	knightsSameFile := "7k/8/8/N7/8/8/8/N3K3 w - - 0 1"
	got1 := sanOf(t, knightsSameFile, "a1b3")
	got5 := sanOf(t, knightsSameFile, "a5b3")
	if !strings.HasPrefix(got1, "N1b3") || !strings.HasPrefix(got5, "N5b3") {
		t.Errorf("same-file knight disambiguation = %q / %q, want N1b3 / N5b3 prefixes", got1, got5)
	}
}

func TestPGNRoundTripsThroughAChessProgramShape(t *testing.T) {
	// A short real game: the Opera Game opening.
	ucis := []string{"e2e4", "e7e5", "g1f3", "d7d6", "d2d4", "c8g4"}
	tags := map[string]string{
		"Event": "Test", "White": "Morphy", "Black": "Duke", "Result": "1-0",
	}
	pgn, err := PGN(StartFEN, ucis, tags)
	if err != nil {
		t.Fatal(err)
	}

	// Header present and quoted.
	for _, want := range []string{`[White "Morphy"]`, `[Black "Duke"]`, `[Result "1-0"]`} {
		if !strings.Contains(pgn, want) {
			t.Errorf("PGN missing header %s\n---\n%s", want, pgn)
		}
	}
	// Move text is SAN with numbers, ending in the result.
	for _, want := range []string{"1. e4 e5", "2. Nf3 d6", "3. d4 Bg4", "1-0"} {
		if !strings.Contains(pgn, want) {
			t.Errorf("PGN missing movetext %q\n---\n%s", want, pgn)
		}
	}
	// A blank line must separate tags from moves (PGN requirement).
	if !strings.Contains(pgn, "]\n\n") {
		t.Errorf("PGN missing blank line between tags and moves\n---\n%s", pgn)
	}
}

func TestPGNRejectsIllegalMove(t *testing.T) {
	if _, err := PGN(StartFEN, []string{"e2e4", "e2e4"}, nil); err == nil {
		t.Error("expected an error for an illegal move in the sequence")
	}
}
