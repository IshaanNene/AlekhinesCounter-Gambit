// Package worker consumes analysis requests and produces game reports.
//
// This is the payoff of the event backbone: a finished game is a message, not a
// blocking call. Workers pull at their own pace, and analysis capacity scales by
// adding members to the consumer group — the game-service neither knows nor
// cares how many there are.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/analysis"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	analysisv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/analysis/v1"
)

// Engine evaluates positions.
type Engine interface {
	Analyze(ctx context.Context, fen string, depth int) (*redisx.Eval, error)
}

// Store persists finished reports.
type Store interface {
	SaveAnalysis(ctx context.Context, report *analysisv1.AnalysisCompleted) error
}

// Worker analyses one game at a time.
type Worker struct {
	engine    Engine
	store     Store
	novelty   *redisx.Novelty
	integrity *redisx.Integrity
	log       *slog.Logger
	depth     int
}

// New builds a worker. novelty and integrity may be disabled.
func New(engine Engine, store Store, novelty *redisx.Novelty, integrity *redisx.Integrity, depth int, log *slog.Logger) *Worker {
	if depth <= 0 {
		depth = 14
	}
	return &Worker{engine: engine, store: store, novelty: novelty, integrity: integrity, depth: depth, log: log}
}

// perPositionTimeout bounds one engine call, so a pathological position cannot
// hold a partition hostage.
const perPositionTimeout = 30 * time.Second

// Analyze evaluates a whole game and returns its report.
//
// One evaluation per position, not two per move. The position after move i *is*
// the position before move i+1, so evaluating each position once gives both
// sides of every move: N+1 calls for N moves rather than 2N.
func (w *Worker) Analyze(ctx context.Context, req *analysisv1.AnalysisRequested) (*analysisv1.AnalysisCompleted, error) {
	fens := req.GetFenHistory()
	moves := req.GetUci()
	if len(fens) < 2 || len(moves) == 0 {
		return nil, fmt.Errorf("game %s: need at least one move, got %d positions and %d moves",
			req.GetGameId(), len(fens), len(moves))
	}
	// The last position has no move out of it, so it is only needed as the
	// "after" of the final move.
	if len(fens) != len(moves)+1 {
		return nil, fmt.Errorf("game %s: %d positions cannot describe %d moves",
			req.GetGameId(), len(fens), len(moves))
	}

	depth := int(req.GetDepth())
	if depth <= 0 {
		depth = w.depth
	}

	evals := make([]analysis.Eval, len(fens))
	for i, fen := range fens {
		e, err := w.evaluate(ctx, fen, depth)
		if err != nil {
			return nil, fmt.Errorf("game %s ply %d: %w", req.GetGameId(), i, err)
		}
		evals[i] = e
	}

	reports := make([]analysis.MoveReport, 0, len(moves))
	for i, uci := range moves {
		before, after := evals[i], evals[i+1]

		// Whose move it was, taken from the position itself rather than assumed
		// from parity — a game need not start with White to move.
		board, err := chess.ParseFEN(fens[i])
		if err != nil {
			return nil, fmt.Errorf("game %s: corrupt fen at ply %d: %w", req.GetGameId(), i+1, err)
		}
		whiteToMove := board.Turn == chess.White

		matched := before.BestMove == uci
		loss := analysis.LossForMove(before, after, matched)
		reports = append(reports, analysis.MoveReport{
			Ply:           i + 1,
			UCI:           uci,
			BestUCI:       before.BestMove,
			EvalBeforeCP:  before.Normalized(),
			EvalAfterCP:   after.Normalized(),
			CentipawnLoss: loss,
			Accuracy:      analysis.AccuracyForMove(before, after, matched),
			Quality:       analysis.Classify(loss, matched, false),
			MatchedEngine: matched,
			MateBefore:    before.Mate,
			MateAfter:     after.Mate,
			WhiteToMove:   whiteToMove,
		})
	}

	white := analysis.Summarise(reports, true)
	black := analysis.Summarise(reports, false)

	out := &analysisv1.AnalysisCompleted{
		GameId:     req.GetGameId(),
		Depth:      uint32(depth),
		Moves:      toProtoMoves(reports),
		White:      toProtoReport(req.GetWhiteId(), white),
		Black:      toProtoReport(req.GetBlackId(), black),
		AnalyzedAt: timestamppb.Now(),
	}

	w.attachNovelty(ctx, out, fens)
	w.recordFairPlay(ctx, req, white, black)
	return out, nil
}

// evaluate asks the engine for one position, through the cache.
func (w *Worker) evaluate(ctx context.Context, fen string, depth int) (analysis.Eval, error) {
	ectx, cancel := context.WithTimeout(ctx, perPositionTimeout)
	defer cancel()

	e, err := w.engine.Analyze(ectx, fen, depth)
	if err != nil {
		return analysis.Eval{}, err
	}
	return analysis.Eval{
		BestMove: e.BestMove,
		ScoreCP:  int(e.ScoreCP),
		Mate:     e.Mate,
		MateIn:   int(e.MateIn),
	}, nil
}

// attachNovelty finds the game's first previously-unseen position and records
// every position for future games.
//
// Order matters: ask *before* recording, or the game would make its own
// positions known and never report a novelty.
func (w *Worker) attachNovelty(ctx context.Context, out *analysisv1.AnalysisCompleted, fens []string) {
	if !w.novelty.Enabled() {
		return
	}
	if fen, idx, found := w.novelty.FirstNovelty(ctx, fens); found {
		out.NoveltyFen = fen
		out.NoveltyPly = uint32(idx)
		w.log.Info("theoretical novelty", "game_id", out.GetGameId(), "ply", idx)
	}
	if err := w.novelty.Record(ctx, fens); err != nil {
		// A missed record only under-reports future novelties.
		w.log.Warn("failed to record positions", "game_id", out.GetGameId(), "error", err)
	}
}

// recordFairPlay stores per-player signals for later review.
//
// Only human-vs-human games. In an engine game the engine's own moves match
// itself perfectly, which would poison the series with meaningless 100% scores,
// and a player using the practice engine is not cheating anyone.
func (w *Worker) recordFairPlay(ctx context.Context, req *analysisv1.AnalysisRequested, white, black analysis.PlayerReport) {
	if !w.integrity.Enabled() || req.GetVsEngine() {
		return
	}
	for _, side := range []struct {
		id string
		r  analysis.PlayerReport
	}{
		{req.GetWhiteId(), white},
		{req.GetBlackId(), black},
	} {
		if side.id == "" {
			continue
		}
		if err := w.integrity.Record(ctx, redisx.GameSignals{
			PlayerID:         side.id,
			EngineMatchRate:  side.r.EngineMatchRate,
			AvgCentipawnLoss: side.r.AvgCentipawnLoss,
			MoveCount:        side.r.MoveCount,
			At:               time.Now(),
		}); err != nil {
			w.log.Warn("failed to record fair-play signals", "player_id", side.id, "error", err)
		}
	}
}

func toProtoMoves(reports []analysis.MoveReport) []*analysisv1.MoveAnalysis {
	out := make([]*analysisv1.MoveAnalysis, 0, len(reports))
	for _, r := range reports {
		out = append(out, &analysisv1.MoveAnalysis{
			Ply:           uint32(r.Ply),
			Uci:           r.UCI,
			BestUci:       r.BestUCI,
			EvalBeforeCp:  int32(r.EvalBeforeCP),
			EvalAfterCp:   int32(r.EvalAfterCP),
			CentipawnLoss: int32(r.CentipawnLoss),
			Quality:       toProtoQuality(r.Quality),
			MatchedEngine: r.MatchedEngine,
			MateBefore:    r.MateBefore,
			MateAfter:     r.MateAfter,
		})
	}
	return out
}

func toProtoReport(playerID string, r analysis.PlayerReport) *analysisv1.PlayerReport {
	return &analysisv1.PlayerReport{
		PlayerId:         playerID,
		EngineMatchRate:  r.EngineMatchRate,
		AvgCentipawnLoss: r.AvgCentipawnLoss,
		Accuracy:         r.Accuracy,
		Blunders:         uint32(r.Blunders),
		Mistakes:         uint32(r.Mistakes),
		Inaccuracies:     uint32(r.Inaccuracies),
		MoveCount:        uint32(r.MoveCount),
	}
}

func toProtoQuality(q analysis.Quality) analysisv1.Quality {
	switch q {
	case analysis.QualityBrilliant:
		return analysisv1.Quality_QUALITY_BRILLIANT
	case analysis.QualityBest:
		return analysisv1.Quality_QUALITY_BEST
	case analysis.QualityExcellent:
		return analysisv1.Quality_QUALITY_EXCELLENT
	case analysis.QualityGood:
		return analysisv1.Quality_QUALITY_GOOD
	case analysis.QualityInaccuracy:
		return analysisv1.Quality_QUALITY_INACCURACY
	case analysis.QualityMistake:
		return analysisv1.Quality_QUALITY_MISTAKE
	case analysis.QualityBlunder:
		return analysisv1.Quality_QUALITY_BLUNDER
	default:
		return analysisv1.Quality_QUALITY_UNSPECIFIED
	}
}
