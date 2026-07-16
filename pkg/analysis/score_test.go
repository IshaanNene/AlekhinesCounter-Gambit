package analysis

import (
	"math"
	"testing"
)

// The perspective rule is the whole point of this package, so pin it down: a
// best move leaves the opponent seeing roughly the mirror of what the mover saw.
func TestCentipawnLossIsZeroForABestMove(t *testing.T) {
	before := Eval{ScoreCP: 50} // mover is +50
	after := Eval{ScoreCP: -50} // opponent now sees -50: nothing was given away
	if loss := CentipawnLoss(before, after); loss != 0 {
		t.Errorf("best move loss = %d, want 0", loss)
	}
}

func TestCentipawnLossMeasuresWhatWasGivenAway(t *testing.T) {
	// Mover was +50; after their move the opponent is +300. They handed over 350.
	before := Eval{ScoreCP: 50}
	after := Eval{ScoreCP: 300}
	if loss := CentipawnLoss(before, after); loss != 350 {
		t.Errorf("loss = %d, want 350", loss)
	}
}

// Engine scores wobble between depths; a "negative loss" is noise, not a gain.
func TestCentipawnLossNeverNegative(t *testing.T) {
	before := Eval{ScoreCP: 50}
	after := Eval{ScoreCP: -80} // opponent worse off than the engine's own best line
	if loss := CentipawnLoss(before, after); loss != 0 {
		t.Errorf("loss = %d, want 0 (a move cannot cost less than nothing)", loss)
	}
}

// One catastrophe in an already-lost game must not swamp a player's average.
func TestCentipawnLossIsCapped(t *testing.T) {
	before := Eval{ScoreCP: 0}
	after := Eval{Mate: true, MateIn: 1} // opponent now mates in 1
	loss := CentipawnLoss(before, after)
	if loss != MaxLoss {
		t.Errorf("loss = %d, want the %d cap", loss, MaxLoss)
	}
}

func TestNormalizedRanksNearerMatesHigher(t *testing.T) {
	m1 := Eval{Mate: true, MateIn: 1}.Normalized()
	m5 := Eval{Mate: true, MateIn: 5}.Normalized()
	if m1 <= m5 {
		t.Errorf("mate in 1 (%d) should outrank mate in 5 (%d)", m1, m5)
	}
	// Being mated is the worst outcome available.
	beingMated := Eval{Mate: true, MateIn: -2}.Normalized()
	if beingMated > -MateScore+1000 {
		t.Errorf("being mated scored %d, expected deeply negative", beingMated)
	}
	if beingMated >= m5 {
		t.Error("being mated must score below mating")
	}
}

// UCI's `score mate 0` means the side to move is already checkmated. Reading it
// as "we mate in 0" scores the mated player +10000, which made delivering
// checkmate score as a 1000-centipawn blunder.
func TestMateZeroMeansTheSideToMoveIsMated(t *testing.T) {
	mated := Eval{Mate: true, MateIn: 0}.Normalized()
	if mated != -MateScore {
		t.Errorf("mate 0 normalised to %d, want %d (the side to move is checkmated)", mated, -MateScore)
	}
	// And the move that delivers mate must be free, not maximally expensive:
	// white is mating (+9900), and after it black is mated (-10000 from black's view).
	before := Eval{Mate: true, MateIn: 1, BestMove: "h5f7"}
	after := Eval{Mate: true, MateIn: 0}
	if loss := CentipawnLoss(before, after); loss != 0 {
		t.Errorf("delivering checkmate cost %d centipawns, want 0", loss)
	}
	// It should also read as near-perfect accuracy, not a catastrophe.
	if acc := AccuracyFor(before, after); acc < 99 {
		t.Errorf("accuracy for delivering mate = %.1f, want ~100", acc)
	}
}

// The engine's own choice is the zero point of centipawn loss. Charging a
// player for playing it is an artifact of evaluating positions independently at
// a fixed depth — a mate invisible from one root appears from the next.
func TestLossForMoveIsZeroWhenPlayingTheEnginesChoice(t *testing.T) {
	// A lost position where the only move still loses: the raw difference is huge.
	before := Eval{ScoreCP: -500, BestMove: "f6d7"}
	after := Eval{Mate: true, MateIn: 2} // opponent mates from here regardless
	if raw := CentipawnLoss(before, after); raw == 0 {
		t.Fatal("expected the raw eval difference to be large; the test is not exercising the case")
	}
	if loss := LossForMove(before, after, true); loss != 0 {
		t.Errorf("playing the engine's own choice cost %d, want 0 — the position was already lost", loss)
	}
	// A different move in the same spot is still measured normally.
	if loss := LossForMove(before, after, false); loss == 0 {
		t.Error("a non-matching move should still be charged")
	}
}

func TestAccuracyForMoveIsPerfectWhenPlayingTheEnginesChoice(t *testing.T) {
	before := Eval{ScoreCP: -500, BestMove: "f6d7"}
	after := Eval{Mate: true, MateIn: 2}
	if acc := AccuracyForMove(before, after, true); acc != 100 {
		t.Errorf("the engine's own choice scored %.1f accuracy, want 100", acc)
	}
	// A different move is still judged on what it surrendered.
	if acc := AccuracyForMove(before, after, false); acc >= 100 {
		t.Errorf("a non-matching move scored %.1f; it should be judged normally", acc)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name    string
		loss    int
		matched bool
		only    bool
		want    Quality
	}{
		{"engine's choice", 0, true, false, QualityBest},
		{"only move, found", 0, true, true, QualityBrilliant},
		{"different but equal", 5, false, false, QualityExcellent},
		{"slightly off", 40, false, false, QualityGood},
		{"inaccuracy", 80, false, false, QualityInaccuracy},
		{"mistake", 200, false, false, QualityMistake},
		{"blunder", 500, false, false, QualityBlunder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.loss, tc.matched, tc.only); got != tc.want {
				t.Errorf("Classify(%d, %v, %v) = %s, want %s", tc.loss, tc.matched, tc.only, got, tc.want)
			}
		})
	}
}

func TestWinPercentIsSymmetricAndBounded(t *testing.T) {
	// A dead-equal position is a coin flip.
	if w := WinPercent(0); math.Abs(w-50) > 0.001 {
		t.Errorf("WinPercent(0) = %.2f, want 50", w)
	}
	// Equal and opposite evaluations must mirror around 50.
	for _, cp := range []float64{50, 200, 800} {
		a, b := WinPercent(cp), WinPercent(-cp)
		if math.Abs((a+b)-100) > 0.001 {
			t.Errorf("WinPercent(%v)+WinPercent(-%v) = %.2f, want 100", cp, cp, a+b)
		}
	}
	// Centipawns are not linear in value: the first 100 matter far more than the
	// hundred between +900 and +1000, both of which are simply winning.
	nearEqual := WinPercent(100) - WinPercent(0)
	alreadyWinning := WinPercent(1000) - WinPercent(900)
	if nearEqual <= alreadyWinning {
		t.Errorf("100cp near equality (%.2f) should matter more than when already winning (%.2f)",
			nearEqual, alreadyWinning)
	}
}

func TestMoveAccuracyIsMonotonicAndBounded(t *testing.T) {
	// Surrendering nothing is perfect.
	if a := MoveAccuracy(0); a < 99 {
		t.Errorf("MoveAccuracy(0) = %.1f, want ~100", a)
	}
	prev := math.Inf(1)
	for _, drop := range []float64{0, 2, 5, 10, 25, 50} {
		a := MoveAccuracy(drop)
		if a > prev {
			t.Errorf("MoveAccuracy(%.0f) = %.1f rose above the previous %.1f", drop, a, prev)
		}
		if a < 0 || a > 100 {
			t.Errorf("MoveAccuracy(%.0f) = %.1f is outside 0..100", drop, a)
		}
		prev = a
	}
}

// The regression this package exists to prevent: a ~15 centipawn game is
// grandmaster play and must not read as a coin flip. It only does if the
// centipawn loss is fed straight into a curve that expects a win% drop.
func TestStrongGameReadsAsStrong(t *testing.T) {
	// +20 for the mover, playing a move that concedes ~15 centipawns.
	before := Eval{ScoreCP: 20}
	after := Eval{ScoreCP: -5} // mover's view: +5, so ~15cp surrendered
	acc := AccuracyFor(before, after)
	if acc < 90 {
		t.Errorf("AccuracyFor(~15cp loss) = %.1f, want >90 for grandmaster-grade play", acc)
	}
	// And a real blunder must read badly.
	blunder := AccuracyFor(Eval{ScoreCP: 50}, Eval{ScoreCP: 600})
	if blunder > 40 {
		t.Errorf("AccuracyFor(blunder) = %.1f, want a low score", blunder)
	}
}

func TestSummariseSplitsBySide(t *testing.T) {
	moves := []MoveReport{
		{Ply: 1, WhiteToMove: true, CentipawnLoss: 0, Accuracy: 100, MatchedEngine: true, Quality: QualityBest},
		{Ply: 2, WhiteToMove: false, CentipawnLoss: 400, Accuracy: 20, Quality: QualityBlunder},
		{Ply: 3, WhiteToMove: true, CentipawnLoss: 20, Accuracy: 92, Quality: QualityGood},
		{Ply: 4, WhiteToMove: false, CentipawnLoss: 150, Accuracy: 55, Quality: QualityMistake},
	}

	white := Summarise(moves, true)
	if white.MoveCount != 2 {
		t.Errorf("white moves = %d, want 2", white.MoveCount)
	}
	if white.EngineMatchRate != 0.5 {
		t.Errorf("white match rate = %.2f, want 0.50", white.EngineMatchRate)
	}
	if white.AvgCentipawnLoss != 10 {
		t.Errorf("white ACPL = %.1f, want 10", white.AvgCentipawnLoss)
	}
	if white.Blunders != 0 {
		t.Errorf("white blunders = %d, want 0", white.Blunders)
	}

	black := Summarise(moves, false)
	if black.Blunders != 1 || black.Mistakes != 1 {
		t.Errorf("black: %d blunders, %d mistakes; want 1 and 1", black.Blunders, black.Mistakes)
	}
	if black.AvgCentipawnLoss != 275 {
		t.Errorf("black ACPL = %.1f, want 275", black.AvgCentipawnLoss)
	}
	// The stronger side must score higher.
	if white.Accuracy <= black.Accuracy {
		t.Errorf("white accuracy %.1f should exceed black's %.1f", white.Accuracy, black.Accuracy)
	}
}

func TestSummariseEmptyIsZero(t *testing.T) {
	r := Summarise(nil, true)
	if r.MoveCount != 0 || r.Accuracy != 0 || r.EngineMatchRate != 0 {
		t.Errorf("empty summary = %+v, want zeroes", r)
	}
}
