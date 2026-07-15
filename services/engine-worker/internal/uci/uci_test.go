package uci

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
)

// enginePath resolves a Stockfish binary, skipping the test if none is present.
func enginePath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("stockfish")
	if err != nil {
		t.Skip("stockfish not found in PATH; skipping engine test")
	}
	return path
}

func TestAnalyzeStartPosition(t *testing.T) {
	e, err := New(enginePath(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := e.Analyze(ctx, chess.StartFEN, 12, 0)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// The best move must be legal in the start position.
	b := chess.NewBoard()
	if _, err := b.ApplyUCI(res.BestMove); err != nil {
		t.Errorf("engine returned illegal move %q: %v", res.BestMove, err)
	}
	if res.Depth == 0 {
		t.Error("expected non-zero search depth")
	}
}

// TestAnalyzeSequentialCalls guards against a regression where a per-search
// reader goroutine outlived the call and stole output from the next search,
// hanging the second Analyze.
func TestAnalyzeSequentialCalls(t *testing.T) {
	e, err := New(enginePath(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()

	board := chess.NewBoard()
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		res, err := e.Analyze(ctx, board.FEN(), 10, 0)
		cancel()
		if err != nil {
			t.Fatalf("Analyze call %d: %v", i, err)
		}
		next, err := board.ApplyUCI(res.BestMove)
		if err != nil {
			t.Fatalf("call %d: engine returned illegal move %q: %v", i, res.BestMove, err)
		}
		board = next
	}
}

func TestAnalyzeFindsMateInOne(t *testing.T) {
	e, err := New(enginePath(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// White to move with a forced mate in one: Qb1-b7#. Black king a8 is boxed
	// in by the white king on b6, and the queen covers the back rank.
	res, err := e.Analyze(ctx, "k7/8/1K6/8/8/8/8/1Q6 w - - 0 1", 0, 500)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.Mate {
		t.Errorf("expected a forced mate to be reported, got score_cp=%d", res.ScoreCP)
	}
}
