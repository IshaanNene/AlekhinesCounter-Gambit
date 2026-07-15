// Package analysis turns raw engine evaluations into a human-readable game
// report: what each move cost, how accurate each player was, and where the game
// turned.
//
// Pure functions, no I/O — the engine calls and storage live in the worker. The
// arithmetic here has a perspective trap in it (engine scores are always from
// the side-to-move's view), so it is worth isolating and testing on its own.
package analysis

import "math"

// MateScore is the centipawn value a forced mate is worth.
//
// Not infinity: a mate in 3 must outrank a mate in 8, and a lost position must
// still be comparable, so mates map onto the same scale as everything else with
// room to distinguish distance.
const MateScore = 10000

// MaxLoss caps how much a single move may be judged to have cost.
//
// Without it, one blunder in an already-lost position (say -20 to mate) would
// swamp a player's average and make every losing game look like an accident.
// The cap keeps average centipawn loss a measure of *play*, not of scoreline.
const MaxLoss = 1000

// Quality classifies a move against the engine's verdict.
type Quality string

const (
	QualityBest       Quality = "BEST"
	QualityExcellent  Quality = "EXCELLENT"
	QualityGood       Quality = "GOOD"
	QualityInaccuracy Quality = "INACCURACY"
	QualityMistake    Quality = "MISTAKE"
	QualityBlunder    Quality = "BLUNDER"
	QualityBrilliant  Quality = "BRILLIANT"
)

// Eval is an engine verdict on a position, always from the side-to-move's view.
type Eval struct {
	BestMove string
	ScoreCP  int
	Mate     bool
	MateIn   int
}

// Normalized converts an evaluation to a plain centipawn score on one scale,
// collapsing mates onto it. Still from the side-to-move's perspective.
func (e Eval) Normalized() int {
	if !e.Mate {
		return clampInt(e.ScoreCP, -MateScore, MateScore)
	}
	// Nearer mates score higher, so M1 beats M5; sign carries who is mating.
	if e.MateIn >= 0 {
		return MateScore - e.MateIn*100
	}
	return -MateScore - e.MateIn*100 // MateIn negative => we are being mated
}

// CentipawnLoss returns what a move cost the player who made it.
//
// The subtlety: engine scores are always from the side to move. `before` is from
// the mover's view; `after` is the resulting position, so it is from the
// *opponent's* view. Negating `after` puts both on the mover's scale, and the
// loss is the difference:
//
//	loss = before - (-after) = before + after
//
// A best move leaves after ≈ -before, so the loss is ~0. A blunder swings after
// positive for the opponent, and the loss grows.
func CentipawnLoss(before, after Eval) int {
	loss := before.Normalized() + after.Normalized()
	if loss < 0 {
		// The position improved beyond the engine's own best line — engine noise
		// between depths, not a gain. A move cannot cost less than nothing.
		return 0
	}
	return clampInt(loss, 0, MaxLoss)
}

// Classify grades a move.
//
// matchedEngine is passed separately rather than inferred from a zero loss:
// several moves often draw equal, and playing a different-but-equal move is not
// the same event as finding the engine's choice.
func Classify(loss int, matchedEngine bool, onlyGoodMove bool) Quality {
	switch {
	case matchedEngine && onlyGoodMove:
		// The only move that holds, and it was found.
		return QualityBrilliant
	case matchedEngine:
		return QualityBest
	case loss <= 10:
		return QualityExcellent
	case loss <= 50:
		return QualityGood
	case loss <= 100:
		return QualityInaccuracy
	case loss <= 300:
		return QualityMistake
	default:
		return QualityBlunder
	}
}

// WinPercent converts a centipawn evaluation into the expected score, 0–100,
// for the side the evaluation belongs to.
//
// This conversion is the point. Centipawns are not linear in *value*: going from
// +0 to +100 changes the likely result enormously, while +900 to +1000 changes
// almost nothing — both are winning. Accuracy must be measured in how much
// winning chance a move threw away, not in raw centipawns.
//
// The logistic constant is Lichess's fit to real game outcomes.
func WinPercent(centipawns float64) float64 {
	return 50 + 50*(2/(1+math.Exp(-0.00368208*centipawns))-1)
}

// MoveAccuracy scores a single move from the winning chance it surrendered.
//
// NOTE the domain: this curve takes a *win-percentage* drop, not centipawns.
// Feeding it centipawns directly reads a 15-centipawn (grandmaster) game as ~50%
// accurate, because 15 win-points and 15 centipawns are wildly different things.
func MoveAccuracy(winPercentDrop float64) float64 {
	if winPercentDrop < 0 {
		winPercentDrop = 0
	}
	acc := 103.1668*math.Exp(-0.04354*winPercentDrop) - 3.1669
	return clampFloat(acc, 0, 100)
}

// AccuracyFor returns a move's accuracy given the evaluations either side of it.
//
// `before` is from the mover's view; `after` is the resulting position and so is
// from the opponent's — negated here to put both on the mover's scale.
func AccuracyFor(before, after Eval) float64 {
	winBefore := WinPercent(float64(before.Normalized()))
	winAfter := WinPercent(float64(-after.Normalized()))
	return MoveAccuracy(winBefore - winAfter)
}

// MoveReport is the verdict on one played move.
type MoveReport struct {
	Ply           int
	UCI           string
	BestUCI       string
	EvalBeforeCP  int
	EvalAfterCP   int
	CentipawnLoss int
	// Accuracy for this move alone, 0–100.
	Accuracy      float64
	Quality       Quality
	MatchedEngine bool
	MateBefore    bool
	MateAfter     bool
	// WhiteToMove identifies whose move this was.
	WhiteToMove bool
}

// PlayerReport summarises one side's play.
type PlayerReport struct {
	EngineMatchRate  float64 // 0..1
	AvgCentipawnLoss float64
	Accuracy         float64 // 0..100
	Blunders         int
	Mistakes         int
	Inaccuracies     int
	MoveCount        int
}

// Summarise aggregates one side's moves.
func Summarise(moves []MoveReport, whiteToMove bool) PlayerReport {
	var r PlayerReport
	var totalLoss int
	var totalAccuracy float64
	var matched int

	for _, m := range moves {
		if m.WhiteToMove != whiteToMove {
			continue
		}
		r.MoveCount++
		totalLoss += m.CentipawnLoss
		totalAccuracy += m.Accuracy
		if m.MatchedEngine {
			matched++
		}
		switch m.Quality {
		case QualityBlunder:
			r.Blunders++
		case QualityMistake:
			r.Mistakes++
		case QualityInaccuracy:
			r.Inaccuracies++
		}
	}
	if r.MoveCount == 0 {
		return r
	}
	r.EngineMatchRate = float64(matched) / float64(r.MoveCount)
	r.AvgCentipawnLoss = float64(totalLoss) / float64(r.MoveCount)
	// Mean of per-move accuracies, not accuracy of the mean loss: the curve is
	// non-linear, so averaging the inputs and averaging the outputs are different
	// numbers, and only the latter reflects how each move actually played.
	r.Accuracy = totalAccuracy / float64(r.MoveCount)
	return r
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
