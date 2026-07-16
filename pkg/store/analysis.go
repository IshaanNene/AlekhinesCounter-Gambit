package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	analysisv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/analysis/v1"
)

// MoveVerdict is the engine's judgement on one played move.
type MoveVerdict struct {
	Ply           int
	UCI           string
	BestUCI       string
	EvalBeforeCP  int
	EvalAfterCP   int
	CentipawnLoss int
	Quality       string
	MatchedEngine bool
	MateBefore    bool
	MateAfter     bool
}

// SideReport summarises one player's play in a game.
type SideReport struct {
	Accuracy     float64
	ACPL         float64
	MatchRate    float64
	Blunders     int
	Mistakes     int
	Inaccuracies int
}

// GameReport is a finished analysis.
type GameReport struct {
	GameID     string
	Depth      int
	NoveltyFEN string // empty when the game contained no novelty
	NoveltyPly int
	White      SideReport
	Black      SideReport
	Moves      []MoveVerdict
	AnalyzedAt time.Time
}

// SaveAnalysis writes a report, replacing any previous one for the game.
//
// Idempotent by construction: Kafka delivers at least once, so the same game can
// legitimately be analysed twice (a worker crash after evaluating but before
// committing its offset). Both the summary and every move upsert, so a replay
// converges on the same rows instead of duplicating or failing.
func (s *Store) SaveAnalysis(ctx context.Context, r *analysisv1.AnalysisCompleted) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rolled back unless committed

	var noveltyFEN *string
	var noveltyPly *int32
	if f := r.GetNoveltyFen(); f != "" {
		noveltyFEN = &f
		p := int32(r.GetNoveltyPly())
		noveltyPly = &p
	}

	w, b := r.GetWhite(), r.GetBlack()
	if _, err := tx.Exec(ctx,
		`INSERT INTO game_analysis (
		     game_id, depth, novelty_fen, novelty_ply,
		     white_accuracy, white_acpl, white_match_rate,
		     white_blunders, white_mistakes, white_inaccuracies,
		     black_accuracy, black_acpl, black_match_rate,
		     black_blunders, black_mistakes, black_inaccuracies,
		     analyzed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16, now())
		 ON CONFLICT (game_id) DO UPDATE SET
		     depth = EXCLUDED.depth,
		     novelty_fen = EXCLUDED.novelty_fen,
		     novelty_ply = EXCLUDED.novelty_ply,
		     white_accuracy = EXCLUDED.white_accuracy,
		     white_acpl = EXCLUDED.white_acpl,
		     white_match_rate = EXCLUDED.white_match_rate,
		     white_blunders = EXCLUDED.white_blunders,
		     white_mistakes = EXCLUDED.white_mistakes,
		     white_inaccuracies = EXCLUDED.white_inaccuracies,
		     black_accuracy = EXCLUDED.black_accuracy,
		     black_acpl = EXCLUDED.black_acpl,
		     black_match_rate = EXCLUDED.black_match_rate,
		     black_blunders = EXCLUDED.black_blunders,
		     black_mistakes = EXCLUDED.black_mistakes,
		     black_inaccuracies = EXCLUDED.black_inaccuracies,
		     analyzed_at = now()`,
		r.GetGameId(), int32(r.GetDepth()), noveltyFEN, noveltyPly,
		w.GetAccuracy(), w.GetAvgCentipawnLoss(), w.GetEngineMatchRate(),
		int32(w.GetBlunders()), int32(w.GetMistakes()), int32(w.GetInaccuracies()),
		b.GetAccuracy(), b.GetAvgCentipawnLoss(), b.GetEngineMatchRate(),
		int32(b.GetBlunders()), int32(b.GetMistakes()), int32(b.GetInaccuracies()),
	); err != nil {
		return fmt.Errorf("upsert game analysis: %w", err)
	}

	// One batch rather than a round trip per move: a 60-move game is 120 rows.
	batch := &pgx.Batch{}
	for _, m := range r.GetMoves() {
		batch.Queue(
			`INSERT INTO move_analysis (
			     game_id, ply, uci, best_uci, eval_before_cp, eval_after_cp,
			     centipawn_loss, quality, matched_engine, mate_before, mate_after)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			 ON CONFLICT (game_id, ply) DO UPDATE SET
			     uci = EXCLUDED.uci,
			     best_uci = EXCLUDED.best_uci,
			     eval_before_cp = EXCLUDED.eval_before_cp,
			     eval_after_cp = EXCLUDED.eval_after_cp,
			     centipawn_loss = EXCLUDED.centipawn_loss,
			     quality = EXCLUDED.quality,
			     matched_engine = EXCLUDED.matched_engine,
			     mate_before = EXCLUDED.mate_before,
			     mate_after = EXCLUDED.mate_after`,
			r.GetGameId(), int32(m.GetPly()), m.GetUci(), m.GetBestUci(),
			m.GetEvalBeforeCp(), m.GetEvalAfterCp(), m.GetCentipawnLoss(),
			qualityName(m.GetQuality()), m.GetMatchedEngine(),
			m.GetMateBefore(), m.GetMateAfter(),
		)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("insert move analysis: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit analysis: %w", err)
	}
	return nil
}

// GetAnalysis loads a game's report. Returns ErrNotFound when it has not been
// analysed — an ordinary state, not a failure.
func (s *Store) GetAnalysis(ctx context.Context, gameID string) (*GameReport, error) {
	r := &GameReport{GameID: gameID}
	var noveltyFEN *string
	var noveltyPly *int32

	err := s.pool.QueryRow(ctx,
		`SELECT depth, novelty_fen, novelty_ply,
		        white_accuracy, white_acpl, white_match_rate,
		        white_blunders, white_mistakes, white_inaccuracies,
		        black_accuracy, black_acpl, black_match_rate,
		        black_blunders, black_mistakes, black_inaccuracies,
		        analyzed_at
		   FROM game_analysis WHERE game_id = $1`, gameID).
		Scan(&r.Depth, &noveltyFEN, &noveltyPly,
			&r.White.Accuracy, &r.White.ACPL, &r.White.MatchRate,
			&r.White.Blunders, &r.White.Mistakes, &r.White.Inaccuracies,
			&r.Black.Accuracy, &r.Black.ACPL, &r.Black.MatchRate,
			&r.Black.Blunders, &r.Black.Mistakes, &r.Black.Inaccuracies,
			&r.AnalyzedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select game analysis: %w", err)
	}
	if noveltyFEN != nil {
		r.NoveltyFEN = *noveltyFEN
	}
	if noveltyPly != nil {
		r.NoveltyPly = int(*noveltyPly)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT ply, uci, best_uci, eval_before_cp, eval_after_cp,
		        centipawn_loss, quality, matched_engine, mate_before, mate_after
		   FROM move_analysis WHERE game_id = $1 ORDER BY ply`, gameID)
	if err != nil {
		return nil, fmt.Errorf("select move analysis: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var m MoveVerdict
		if err := rows.Scan(&m.Ply, &m.UCI, &m.BestUCI, &m.EvalBeforeCP, &m.EvalAfterCP,
			&m.CentipawnLoss, &m.Quality, &m.MatchedEngine, &m.MateBefore, &m.MateAfter); err != nil {
			return nil, fmt.Errorf("scan move analysis: %w", err)
		}
		r.Moves = append(r.Moves, m)
	}
	return r, rows.Err()
}

// GameForAnalysis returns what a worker needs to analyse a game: the positions
// reached and the moves between them.
func (s *Store) GameForAnalysis(ctx context.Context, gameID string) (fens, ucis []string, err error) {
	g, moves, err := s.GetGame(ctx, gameID)
	if err != nil {
		return nil, nil, err
	}
	// The starting position is not stored on a move row, so the history is the
	// initial FEN followed by each move's resulting position.
	fens = make([]string, 0, len(moves)+1)
	fens = append(fens, startFENFor(g))
	for _, m := range moves {
		fens = append(fens, m.FENAfter)
		ucis = append(ucis, m.UCI)
	}
	return fens, ucis, nil
}

// startFENFor returns the position a game began from.
//
// Every game currently starts from the standard position, so this is a constant
// — but it is the single place a from-position or Chess960 game would change,
// rather than a literal scattered through the analysis path.
func startFENFor(_ *Game) string { return chess.StartFEN }

// qualityName maps the proto enum to the short token stored in the database.
func qualityName(q analysisv1.Quality) string {
	switch q {
	case analysisv1.Quality_QUALITY_BRILLIANT:
		return "BRILLIANT"
	case analysisv1.Quality_QUALITY_BEST:
		return "BEST"
	case analysisv1.Quality_QUALITY_EXCELLENT:
		return "EXCELLENT"
	case analysisv1.Quality_QUALITY_GOOD:
		return "GOOD"
	case analysisv1.Quality_QUALITY_INACCURACY:
		return "INACCURACY"
	case analysisv1.Quality_QUALITY_MISTAKE:
		return "MISTAKE"
	case analysisv1.Quality_QUALITY_BLUNDER:
		return "BLUNDER"
	default:
		return "UNSPECIFIED"
	}
}
