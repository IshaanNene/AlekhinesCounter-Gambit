package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/rating"
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
)

// ListGames returns a user's game history, most recent first.
func (s *Server) ListGames(ctx context.Context, req *gamev1.ListGamesRequest) (*gamev1.ListGamesResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	games, err := s.store.ListGamesForUser(ctx, req.GetUserId(), int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list games: %v", err)
	}
	total, err := s.store.CountGamesForUser(ctx, req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "count games: %v", err)
	}

	out := make([]*gamev1.GameSummary, 0, len(games))
	for _, g := range games {
		sum := &gamev1.GameSummary{
			Id:        g.ID,
			WhiteId:   g.WhiteID,
			WhiteName: g.WhiteName,
			BlackId:   g.BlackID,
			BlackName: g.BlackName,
			VsEngine:  g.VsEngine,
			Rated:     g.Rated,
			Status:    statusFromDB(g.Status),
			EndReason: endReasonFromDB(g.EndReason),
			MoveCount: int32(g.MoveCount),
			StartedAt: timestamppb.New(g.StartedAt),
		}
		// A nil delta means unrated or not yet scored — distinct from a genuine
		// zero, which is why the flag exists rather than relying on 0.
		if g.EloDelta != nil {
			sum.EloDelta = int32(*g.EloDelta)
			sum.HasEloDelta = true
		}
		if g.EndedAt != nil {
			sum.EndedAt = timestamppb.New(*g.EndedAt)
		}
		out = append(out, sum)
	}
	return &gamev1.ListGamesResponse{Games: out, Total: int32(total)}, nil
}

// Leaderboard returns the highest-rated accounts.
func (s *Server) Leaderboard(ctx context.Context, req *gamev1.LeaderboardRequest) (*gamev1.LeaderboardResponse, error) {
	entries, err := s.store.Leaderboard(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "leaderboard: %v", err)
	}
	out := make([]*gamev1.LeaderboardEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, &gamev1.LeaderboardEntry{
			Rank:        int32(e.Rank),
			UserId:      e.UserID,
			Username:    e.Username,
			Elo:         int32(e.Elo),
			GamesPlayed: int32(e.GamesPlayed),
		})
	}
	return &gamev1.LeaderboardResponse{Entries: out}, nil
}

// applyRatings scores a finished rated game. Errors are logged, not returned:
// the result is already durable, and failing the move that ended the game would
// be a worse outcome than a rating that needs backfilling.
func (s *Server) applyRatings(ctx context.Context, gameID string, result chess.Result) {
	var outcome rating.Outcome
	switch result {
	case chess.WhiteWins:
		outcome = rating.WhiteWon
	case chess.BlackWins:
		outcome = rating.BlackWon
	case chess.Draw:
		outcome = rating.Drawn
	default:
		return // still in progress: nothing to score
	}

	applied, err := s.store.ApplyRatings(ctx, gameID, outcome)
	if err != nil {
		s.log.Error("failed to apply ratings", "game_id", gameID, "error", err)
		return
	}
	if applied {
		s.log.Info("ratings applied", "game_id", gameID, "outcome", result)
	}
}
