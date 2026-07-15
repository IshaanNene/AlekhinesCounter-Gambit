package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	analysisv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/analysis/v1"
)

// fakeEngine answers from a script, and counts how often it was asked.
type fakeEngine struct {
	mu    sync.Mutex
	calls []string
	byFEN map[string]*redisx.Eval
	def   *redisx.Eval
}

func (f *fakeEngine) Analyze(_ context.Context, fen string, _ int) (*redisx.Eval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fen)
	if e, ok := f.byFEN[fen]; ok {
		return e, nil
	}
	return f.def, nil
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// playGame returns the fen history and moves for a real sequence, so tests use
// positions the chess package actually accepts.
func playGame(t *testing.T, ucis ...string) (fens []string, moves []string) {
	t.Helper()
	b := chess.NewBoard()
	fens = append(fens, b.FEN())
	for _, u := range ucis {
		nb, err := b.ApplyUCI(u)
		if err != nil {
			t.Fatalf("apply %s: %v", u, err)
		}
		b = nb
		fens = append(fens, b.FEN())
		moves = append(moves, u)
	}
	return fens, moves
}

// The optimisation that halves the engine work: the position after move i is the
// position before move i+1, so each position is evaluated exactly once.
func TestAnalyzeEvaluatesEachPositionOnce(t *testing.T) {
	fens, moves := playGame(t, "e2e4", "e7e5", "g1f3")
	eng := &fakeEngine{def: &redisx.Eval{BestMove: "zzzz", ScoreCP: 0}}
	w := New(eng, nil, redisx.NewNovelty(nil), redisx.NewIntegrity(nil), 12, quietLog())

	_, err := w.Analyze(context.Background(), &analysisv1.AnalysisRequested{
		GameId: "g1", FenHistory: fens, Uci: moves, WhiteId: "w", BlackId: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3 moves => 4 positions => 4 calls, not 6.
	if len(eng.calls) != len(fens) {
		t.Errorf("engine called %d times for %d positions (%d moves); want one per position",
			len(eng.calls), len(fens), len(moves))
	}
	// And never the same position twice.
	seen := map[string]bool{}
	for _, c := range eng.calls {
		if seen[c] {
			t.Errorf("position evaluated twice: %s", c)
		}
		seen[c] = true
	}
}

func TestAnalyzeAttributesMovesToTheRightSide(t *testing.T) {
	fens, moves := playGame(t, "e2e4", "e7e5", "g1f3", "b8c6")
	eng := &fakeEngine{def: &redisx.Eval{BestMove: "zzzz", ScoreCP: 0}}
	w := New(eng, nil, redisx.NewNovelty(nil), redisx.NewIntegrity(nil), 12, quietLog())

	out, err := w.Analyze(context.Background(), &analysisv1.AnalysisRequested{
		GameId: "g1", FenHistory: fens, Uci: moves, WhiteId: "w", BlackId: "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.GetMoves()) != 4 {
		t.Fatalf("got %d move reports, want 4", len(out.GetMoves()))
	}
	// Two moves each, taken from the side to move in each position.
	if out.GetWhite().GetMoveCount() != 2 {
		t.Errorf("white move count = %d, want 2", out.GetWhite().GetMoveCount())
	}
	if out.GetBlack().GetMoveCount() != 2 {
		t.Errorf("black move count = %d, want 2", out.GetBlack().GetMoveCount())
	}
	if out.GetWhite().GetPlayerId() != "w" || out.GetBlack().GetPlayerId() != "b" {
		t.Error("player ids not carried onto the reports")
	}
}

// A player who plays the engine's move every time must score 100% match.
func TestAnalyzeDetectsEngineMatches(t *testing.T) {
	fens, moves := playGame(t, "e2e4", "e7e5")
	eng := &fakeEngine{
		byFEN: map[string]*redisx.Eval{
			fens[0]: {BestMove: "e2e4", ScoreCP: 20},  // white plays the top choice
			fens[1]: {BestMove: "e7e5", ScoreCP: -20}, // so does black
			fens[2]: {BestMove: "g1f3", ScoreCP: 20},
		},
		def: &redisx.Eval{BestMove: "zzzz"},
	}
	w := New(eng, nil, redisx.NewNovelty(nil), redisx.NewIntegrity(nil), 12, quietLog())

	out, err := w.Analyze(context.Background(), &analysisv1.AnalysisRequested{
		GameId: "g1", FenHistory: fens, Uci: moves, WhiteId: "w", BlackId: "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.GetWhite().GetEngineMatchRate() != 1 {
		t.Errorf("white match rate = %.2f, want 1.0", out.GetWhite().GetEngineMatchRate())
	}
	if out.GetMoves()[0].GetQuality() != analysisv1.Quality_QUALITY_BEST {
		t.Errorf("move 1 quality = %v, want BEST", out.GetMoves()[0].GetQuality())
	}
	if !out.GetMoves()[0].GetMatchedEngine() {
		t.Error("move 1 should be marked as matching the engine")
	}
}

// A move that hands the opponent a large advantage is a blunder.
func TestAnalyzeDetectsABlunder(t *testing.T) {
	fens, moves := playGame(t, "e2e4", "e7e5")
	eng := &fakeEngine{
		byFEN: map[string]*redisx.Eval{
			fens[0]: {BestMove: "d2d4", ScoreCP: 30},  // white was +30, played something else
			fens[1]: {BestMove: "d7d5", ScoreCP: 500}, // black is now +500: white gave away 530
			fens[2]: {BestMove: "g1f3", ScoreCP: 0},
		},
		def: &redisx.Eval{BestMove: "zzzz"},
	}
	w := New(eng, nil, redisx.NewNovelty(nil), redisx.NewIntegrity(nil), 12, quietLog())

	out, err := w.Analyze(context.Background(), &analysisv1.AnalysisRequested{
		GameId: "g1", FenHistory: fens, Uci: moves, WhiteId: "w", BlackId: "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	first := out.GetMoves()[0]
	if first.GetCentipawnLoss() != 530 {
		t.Errorf("centipawn loss = %d, want 530", first.GetCentipawnLoss())
	}
	if first.GetQuality() != analysisv1.Quality_QUALITY_BLUNDER {
		t.Errorf("quality = %v, want BLUNDER", first.GetQuality())
	}
	if out.GetWhite().GetBlunders() != 1 {
		t.Errorf("white blunders = %d, want 1", out.GetWhite().GetBlunders())
	}
}

func TestAnalyzeRejectsMismatchedInput(t *testing.T) {
	eng := &fakeEngine{def: &redisx.Eval{BestMove: "zzzz"}}
	w := New(eng, nil, redisx.NewNovelty(nil), redisx.NewIntegrity(nil), 12, quietLog())

	cases := []struct {
		name string
		req  *analysisv1.AnalysisRequested
	}{
		{"no moves", &analysisv1.AnalysisRequested{GameId: "g", FenHistory: []string{"a", "b"}}},
		{"no positions", &analysisv1.AnalysisRequested{GameId: "g", Uci: []string{"e2e4"}}},
		{
			// N moves need exactly N+1 positions; anything else is a corrupt event.
			"positions do not describe the moves",
			&analysisv1.AnalysisRequested{GameId: "g", FenHistory: []string{"a", "b"}, Uci: []string{"e2e4", "e7e5"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := w.Analyze(context.Background(), tc.req); err == nil {
				t.Error("expected an error for malformed input")
			}
		})
	}
}

// Disabled Redis must not stop a game being analysed — novelty and fair-play
// are enrichments, not requirements.
func TestAnalyzeWorksWithoutRedis(t *testing.T) {
	fens, moves := playGame(t, "e2e4", "e7e5")
	eng := &fakeEngine{def: &redisx.Eval{BestMove: "zzzz", ScoreCP: 0}}
	w := New(eng, nil, redisx.NewNovelty(nil), redisx.NewIntegrity(nil), 12, quietLog())

	out, err := w.Analyze(context.Background(), &analysisv1.AnalysisRequested{
		GameId: "g1", FenHistory: fens, Uci: moves, WhiteId: "w", BlackId: "b",
	})
	if err != nil {
		t.Fatalf("analysis should not need redis: %v", err)
	}
	if out.GetNoveltyFen() != "" {
		t.Error("no novelty should be reported without the filter")
	}
	if len(out.GetMoves()) != 2 {
		t.Errorf("got %d moves, want 2", len(out.GetMoves()))
	}
}
