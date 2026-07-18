package store

import (
	"context"
	"os"
	"testing"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
)

// testStore connects using ACG_TEST_DSN, skipping when it is unset. The target
// database must already have the migrations applied.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ACG_TEST_DSN")
	if dsn == "" {
		t.Skip("ACG_TEST_DSN not set; skipping store integration test")
	}
	st, err := Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestCreateAndGetGame(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	white, err := st.CreateGuestUser(ctx)
	if err != nil {
		t.Fatalf("CreateGuestUser: %v", err)
	}

	g, err := st.CreateGame(ctx, CreateGameParams{
		WhiteID:     white,
		VsEngine:    true,
		EngineDepth: 10,
		StartFEN:    chess.StartFEN,
	})
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}
	if !g.VsEngine {
		t.Error("expected VsEngine=true for an engine game")
	}
	if g.AwaitingOpponent {
		t.Error("an engine game is never awaiting an opponent")
	}

	// Apply a move and persist it.
	board := chess.NewBoard()
	after, _ := board.ApplyUCI("e2e4")
	if err := st.AppendMove(ctx, g.ID, Move{Ply: 1, UCI: "e2e4", FENAfter: after.FEN()}, "IN_PROGRESS", "", false); err != nil {
		t.Fatalf("AppendMove: %v", err)
	}

	got, moves, err := st.GetGame(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetGame: %v", err)
	}
	if got.FEN != after.FEN() {
		t.Errorf("game fen = %q, want %q", got.FEN, after.FEN())
	}
	if len(moves) != 1 || moves[0].UCI != "e2e4" {
		t.Errorf("moves = %+v, want one e2e4", moves)
	}

	// Second guest must not collide on username.
	if _, err := st.CreateGuestUser(ctx); err != nil {
		t.Errorf("second CreateGuestUser: %v", err)
	}
}

// TestAppendMoveWritesOutbox verifies the transactional outbox: a persisted move
// leaves exactly one unpublished outbox row carrying its details, and marking it
// published removes it from the relay's view.
func TestAppendMoveWritesOutbox(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	white, err := st.CreateGuestUser(ctx)
	if err != nil {
		t.Fatalf("CreateGuestUser: %v", err)
	}
	g, err := st.CreateGame(ctx, CreateGameParams{WhiteID: white, VsEngine: true, StartFEN: chess.StartFEN})
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	board := chess.NewBoard()
	after, _ := board.ApplyUCI("e2e4")
	if err := st.AppendMove(ctx, g.ID, Move{Ply: 1, UCI: "e2e4", FENAfter: after.FEN()}, "IN_PROGRESS", "", false); err != nil {
		t.Fatalf("AppendMove: %v", err)
	}

	rows, err := st.FetchUnpublished(ctx, 100)
	if err != nil {
		t.Fatalf("FetchUnpublished: %v", err)
	}
	var row *OutboxRow
	for i := range rows {
		if rows[i].GameID == g.ID {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no unpublished outbox row for game %s", g.ID)
	}
	if row.Ply != 1 || row.UCI != "e2e4" || row.FENAfter != after.FEN() || row.Status != "IN_PROGRESS" || row.Ended {
		t.Errorf("outbox row = %+v, want ply 1 e2e4 IN_PROGRESS not-ended", *row)
	}

	// Once marked, the relay no longer sees it.
	if err := st.MarkPublished(ctx, []int64{row.ID}); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	rows, err = st.FetchUnpublished(ctx, 100)
	if err != nil {
		t.Fatalf("FetchUnpublished after mark: %v", err)
	}
	for _, r := range rows {
		if r.ID == row.ID {
			t.Errorf("outbox row %d still unpublished after MarkPublished", row.ID)
		}
	}
}

func TestGetGameNotFound(t *testing.T) {
	st := testStore(t)
	_, _, err := st.GetGame(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != ErrNotFound {
		t.Errorf("GetGame(missing) = %v, want ErrNotFound", err)
	}
}
