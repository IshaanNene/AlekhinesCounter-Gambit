package graph

import (
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
	sessionv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/session/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/model"
)

// toModelGame converts a game-service proto Game into the GraphQL model.
func toModelGame(g *gamev1.Game) *model.Game {
	if g == nil {
		return nil
	}
	out := &model.Game{
		ID:        g.GetId(),
		Fen:       g.GetFen(),
		Status:    toModelStatus(g.GetStatus()),
		VsEngine:  g.GetVsEngine(),
		WhiteID:   g.GetWhiteId(),
		Moves:     make([]*model.Move, 0, len(g.GetMoves())),
		StartedAt: g.GetStartedAt().AsTime(),
	}
	if b := g.GetBlackId(); b != "" {
		out.BlackID = &b
	}
	if r := toModelEndReason(g.GetEndReason()); r != nil {
		out.EndReason = r
	}
	if g.GetEndedAt() != nil {
		t := g.GetEndedAt().AsTime()
		out.EndedAt = &t
	}
	for _, m := range g.GetMoves() {
		out.Moves = append(out.Moves, &model.Move{
			Ply:      int(m.GetPly()),
			Uci:      m.GetUci(),
			FenAfter: m.GetFenAfter(),
		})
	}
	return out
}

func toModelStatus(s gamev1.Status) model.GameStatus {
	switch s {
	case gamev1.Status_STATUS_WHITE_WON:
		return model.GameStatusWhiteWon
	case gamev1.Status_STATUS_BLACK_WON:
		return model.GameStatusBlackWon
	case gamev1.Status_STATUS_DRAW:
		return model.GameStatusDraw
	default:
		return model.GameStatusInProgress
	}
}

// toModelEndReason returns nil while a game is still in progress.
func toModelEndReason(r gamev1.EndReason) *model.EndReason {
	var out model.EndReason
	switch r {
	case gamev1.EndReason_END_REASON_CHECKMATE:
		out = model.EndReasonCheckmate
	case gamev1.EndReason_END_REASON_STALEMATE:
		out = model.EndReasonStalemate
	case gamev1.EndReason_END_REASON_INSUFFICIENT_MATERIAL:
		out = model.EndReasonInsufficientMaterial
	case gamev1.EndReason_END_REASON_FIFTY_MOVE:
		out = model.EndReasonFiftyMove
	case gamev1.EndReason_END_REASON_THREEFOLD:
		out = model.EndReasonThreefold
	case gamev1.EndReason_END_REASON_RESIGNATION:
		out = model.EndReasonResignation
	default:
		return nil
	}
	return &out
}

// toModelClock converts a session snapshot into the GraphQL Clock.
func toModelClock(s *sessionv1.Snapshot) *model.Clock {
	if s == nil {
		return nil
	}
	return &model.Clock{
		WhiteMs: int(s.GetWhiteMs()),
		BlackMs: int(s.GetBlackMs()),
		Turn:    toModelSide(s.GetTurn()),
		Running: s.GetStatus() == sessionv1.SessionStatus_SESSION_STATUS_IN_PROGRESS,
	}
}

func toModelSide(s sessionv1.Side) model.Side {
	if s == sessionv1.Side_SIDE_BLACK {
		return model.SideBlack
	}
	return model.SideWhite
}
