package rating

import "testing"

func TestExpectedIsSymmetric(t *testing.T) {
	// Equal ratings are a coin flip.
	if got := Expected(1200, 1200); got != 0.5 {
		t.Errorf("Expected(1200,1200) = %v, want 0.5", got)
	}
	// The two sides' expectations must always sum to exactly one game.
	for _, pair := range [][2]int{{1200, 1400}, {800, 2400}, {1500, 1505}} {
		a, b := Expected(pair[0], pair[1]), Expected(pair[1], pair[0])
		if diff := a + b - 1; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("Expected(%d,%d)+Expected(%d,%d) = %v, want 1",
				pair[0], pair[1], pair[1], pair[0], a+b)
		}
	}
	// A 400-point edge is the canonical ~10:1 (0.909) favourite.
	if got := Expected(1600, 1200); got < 0.9089 || got > 0.9095 {
		t.Errorf("Expected(1600,1200) = %v, want ≈0.909", got)
	}
}

func TestUpdateZeroSumForEqualKFactors(t *testing.T) {
	// With both players on the same K, Elo is zero-sum: one side's gain is the
	// other's loss.
	cases := []struct {
		name    string
		outcome Outcome
	}{{"white wins", WhiteWon}, {"black wins", BlackWon}, {"draw", Drawn}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, b := Update(1500, 1500, tc.outcome, 100, 100)
			if w+b != 0 {
				t.Errorf("deltas %d/%d do not sum to zero", w, b)
			}
		})
	}
}

func TestWinnerGainsLoserLoses(t *testing.T) {
	w, b := Update(1500, 1500, WhiteWon, 100, 100)
	if w <= 0 {
		t.Errorf("white won but delta = %d, want > 0", w)
	}
	if b >= 0 {
		t.Errorf("black lost but delta = %d, want < 0", b)
	}
	// Even players, K=20: the winner takes half of K.
	if w != 10 {
		t.Errorf("white delta = %d, want 10", w)
	}
}

func TestUpsetMovesRatingMoreThanExpectedResult(t *testing.T) {
	// The favourite beating the underdog: small gain.
	expected, _ := Update(1800, 1200, WhiteWon, 100, 100)
	// The underdog beating the favourite: large gain.
	upset, _ := Update(1200, 1800, WhiteWon, 100, 100)
	if upset <= expected {
		t.Errorf("upset gain %d should exceed expected-result gain %d", upset, expected)
	}
}

func TestDrawFavoursTheUnderdog(t *testing.T) {
	// A draw against a much stronger player should still gain the weaker one rating.
	w, b := Update(1200, 1800, Drawn, 100, 100)
	if w <= 0 {
		t.Errorf("underdog drew but delta = %d, want > 0", w)
	}
	if b >= 0 {
		t.Errorf("favourite drew but delta = %d, want < 0", b)
	}
}

func TestKFactorTiers(t *testing.T) {
	if got := KFactor(1200, 5); got != 40 {
		t.Errorf("provisional K = %v, want 40", got)
	}
	if got := KFactor(1200, 100); got != 20 {
		t.Errorf("established K = %v, want 20", got)
	}
	if got := KFactor(2500, 100); got != 10 {
		t.Errorf("master K = %v, want 10", got)
	}
	// Provisional beats the master tier: a new account is volatile regardless.
	if got := KFactor(2500, 5); got != 40 {
		t.Errorf("provisional master K = %v, want 40", got)
	}
}

func TestRatingsStayBounded(t *testing.T) {
	// Repeatedly losing to a peer must not run away to absurd values: as the
	// gap widens, further losses cost less and less.
	elo, opp := 1500, 1500
	for i := 0; i < 500; i++ {
		d, _ := Update(elo, opp, BlackWon, 100, 100)
		elo += d
	}
	if elo < 0 {
		t.Errorf("rating went negative: %d", elo)
	}
}
