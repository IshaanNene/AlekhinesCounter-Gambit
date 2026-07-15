package server

import (
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
)

// resultToDB maps a chess result to the DB/proto status string.
func resultToDB(r chess.Result) string {
	switch r {
	case chess.WhiteWins:
		return "WHITE_WON"
	case chess.BlackWins:
		return "BLACK_WON"
	case chess.Draw:
		return "DRAW"
	default:
		return "IN_PROGRESS"
	}
}

// reasonToDB maps a chess end reason to the DB string ("" while ongoing).
func reasonToDB(r chess.EndReason) string {
	switch r {
	case chess.Checkmate:
		return "CHECKMATE"
	case chess.Stalemate:
		return "STALEMATE"
	case chess.InsufficientMaterial:
		return "INSUFFICIENT_MATERIAL"
	case chess.FiftyMove:
		return "FIFTY_MOVE"
	case chess.ThreefoldRepetition:
		return "THREEFOLD"
	default:
		return ""
	}
}

// reasonToSession maps a chess end reason to the lowercase token the
// session-manager reports in its snapshots (alongside "flag", "resignation",
// and "abandonment", which the session decides on its own).
func reasonToSession(r chess.EndReason) string {
	switch r {
	case chess.Checkmate:
		return "checkmate"
	case chess.Stalemate:
		return "stalemate"
	case chess.InsufficientMaterial:
		return "insufficient_material"
	case chess.FiftyMove:
		return "fifty_move"
	case chess.ThreefoldRepetition:
		return "threefold"
	default:
		return "ended"
	}
}

// statusFromDB maps a DB status string to the proto enum.
func statusFromDB(s string) gamev1.Status {
	switch s {
	case "WHITE_WON":
		return gamev1.Status_STATUS_WHITE_WON
	case "BLACK_WON":
		return gamev1.Status_STATUS_BLACK_WON
	case "DRAW":
		return gamev1.Status_STATUS_DRAW
	case "IN_PROGRESS":
		return gamev1.Status_STATUS_IN_PROGRESS
	default:
		return gamev1.Status_STATUS_UNSPECIFIED
	}
}

// endReasonFromDB maps a DB end-reason string to the proto enum.
func endReasonFromDB(s string) gamev1.EndReason {
	switch s {
	case "CHECKMATE":
		return gamev1.EndReason_END_REASON_CHECKMATE
	case "STALEMATE":
		return gamev1.EndReason_END_REASON_STALEMATE
	case "INSUFFICIENT_MATERIAL":
		return gamev1.EndReason_END_REASON_INSUFFICIENT_MATERIAL
	case "FIFTY_MOVE":
		return gamev1.EndReason_END_REASON_FIFTY_MOVE
	case "THREEFOLD":
		return gamev1.EndReason_END_REASON_THREEFOLD
	case "RESIGNATION":
		return gamev1.EndReason_END_REASON_RESIGNATION
	default:
		return gamev1.EndReason_END_REASON_UNSPECIFIED
	}
}
