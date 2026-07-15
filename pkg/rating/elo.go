// Package rating implements Elo rating updates.
//
// Pure functions, no I/O: given two ratings and a result, return the deltas.
// Kept separate from storage so the formula can be tested exhaustively and
// swapped (for Glicko-2, say) without touching the game service.
package rating

import "math"

// DefaultRating is what a new player starts on.
const DefaultRating = 1200

// Outcome is the result of a game from White's perspective.
type Outcome float64

const (
	BlackWon Outcome = 0.0
	Drawn    Outcome = 0.5
	WhiteWon Outcome = 1.0
)

// KFactor controls how fast ratings move. Higher = more volatile.
//
// The tiers follow standard practice: provisional players move fast so they
// reach their true strength quickly, established players move slowly so a
// single result cannot swing a long record, and masters slower still.
func KFactor(rating, gamesPlayed int) float64 {
	switch {
	case gamesPlayed < 30:
		return 40 // provisional: converge quickly
	case rating >= 2400:
		return 10 // established master: stable
	default:
		return 20
	}
}

// Expected returns the probability that a player rated `a` scores against `b`,
// per the logistic Elo curve: a 400-point edge implies ~10:1 odds.
func Expected(a, b int) float64 {
	return 1 / (1 + math.Pow(10, float64(b-a)/400))
}

// Update returns the rating change for each side after a game.
//
// whiteGames/blackGames are each player's completed-game counts, used only to
// pick the K-factor. Deltas are rounded half away from zero, so a player can
// never gain or lose a fractional point.
func Update(whiteElo, blackElo int, outcome Outcome, whiteGames, blackGames int) (whiteDelta, blackDelta int) {
	expWhite := Expected(whiteElo, blackElo)
	expBlack := 1 - expWhite

	scoreWhite := float64(outcome)
	scoreBlack := 1 - scoreWhite

	whiteDelta = roundHalfAway(KFactor(whiteElo, whiteGames) * (scoreWhite - expWhite))
	blackDelta = roundHalfAway(KFactor(blackElo, blackGames) * (scoreBlack - expBlack))
	return whiteDelta, blackDelta
}

// roundHalfAway rounds to the nearest integer, ties away from zero.
func roundHalfAway(f float64) int {
	if f < 0 {
		return -int(math.Floor(-f + 0.5))
	}
	return int(math.Floor(f + 0.5))
}
