package server

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/kafkax"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/objstore"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/rating"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
	analysisv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/analysis/v1"
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

// requestAnalysis asks the worker pool to evaluate a finished game.
//
// Fire-and-forget by design: the game is already durable and the player is
// waiting on their move's response. Analysis is a background enrichment, so a
// Kafka hiccup must cost a report, never the move.
//
// The whole position history rides in the event, so a worker can start without
// reading the database — the queue carries the work, not a pointer to it.
func (s *Server) requestAnalysis(ctx context.Context, gameID string) {
	if !s.events.Enabled() {
		return
	}
	g, _, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		s.log.Warn("cannot request analysis: game unreadable", "game_id", gameID, "error", err)
		return
	}
	fens, ucis, err := s.store.GameForAnalysis(ctx, gameID)
	if err != nil {
		s.log.Warn("cannot request analysis: history unreadable", "game_id", gameID, "error", err)
		return
	}
	if len(ucis) == 0 {
		return // nothing was played; nothing to analyse
	}

	req := &analysisv1.AnalysisRequested{
		GameId:     gameID,
		Depth:      uint32(s.analysisDepth),
		FenHistory: fens,
		Uci:        ucis,
		WhiteId:    g.WhiteID,
		BlackId:    g.BlackID,
		VsEngine:   g.VsEngine,
	}
	// Bounded: the caller is a player mid-move, not a batch job.
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.events.Publish(pctx, kafkax.TopicAnalysisRequested, gameID, req); err != nil {
		s.log.Warn("failed to queue analysis", "game_id", gameID, "error", err)
	}
}

// ingestOpening folds a finished game into the opening explorer statistics.
//
// Fire-and-forget: the explorer is a derived aggregate, so a missed ingest costs
// one game's worth of stats, never the move. Idempotent in the store, so a retry
// is harmless.
func (s *Server) ingestOpening(ctx context.Context, gameID, status string) {
	fens, ucis, err := s.store.GameForAnalysis(ctx, gameID)
	if err != nil || len(ucis) == 0 {
		return
	}
	octx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.store.IngestGameOpening(octx, gameID, status, fens, ucis); err != nil {
		s.log.Warn("could not ingest opening", "game_id", gameID, "error", err)
	}
}

// archivePGN writes a finished game's PGN to object storage.
//
// Best-effort and fire-and-forget: the archive is a convenience for export and
// history download, never the source of truth (Postgres is). A store outage must
// not fail the move that ended the game.
func (s *Server) archivePGN(ctx context.Context, gameID string) {
	if !s.objects.Enabled() {
		return
	}
	g, moves, err := s.store.GetGame(ctx, gameID)
	if err != nil || len(moves) == 0 {
		return
	}

	ucis := make([]string, 0, len(moves))
	for _, m := range moves {
		ucis = append(ucis, m.UCI)
	}
	white, black := "Guest", "Guest"
	if u, err := s.store.GetUser(ctx, g.WhiteID); err == nil {
		white = u.Username
	}
	if g.VsEngine {
		black = "Stockfish"
	} else if u, err := s.store.GetUser(ctx, g.BlackID); err == nil {
		black = u.Username
	}

	pgn, err := chess.PGN(chess.StartFEN, ucis, map[string]string{
		"Event":  "Alekhine's Counter-Gambit",
		"Site":   "localhost",
		"Date":   g.StartedAt.Format("2006.01.02"),
		"White":  white,
		"Black":  black,
		"Result": pgnResult(g.Status),
	})
	if err != nil {
		s.log.Warn("could not render pgn", "game_id", gameID, "error", err)
		return
	}

	octx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.objects.Put(octx, objstore.BucketPGN, gameID+".pgn", []byte(pgn), "application/x-chess-pgn"); err != nil {
		s.log.Warn("could not archive pgn", "game_id", gameID, "error", err)
	}
}

// pgnResult maps a stored status to the PGN result token.
func pgnResult(status string) string {
	switch status {
	case "WHITE_WON":
		return "1-0"
	case "BLACK_WON":
		return "0-1"
	case "DRAW":
		return "1/2-1/2"
	default:
		return "*"
	}
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

// OpeningExplorer returns the moves played from a position, most popular first.
func (s *Server) OpeningExplorer(ctx context.Context, req *gamev1.OpeningExplorerRequest) (*gamev1.OpeningExplorerResponse, error) {
	fen := req.GetFen()
	if fen == "" {
		fen = chess.StartFEN
	}
	// Validate the FEN so a garbage key cannot be probed; also normalises it.
	if _, err := chess.ParseFEN(fen); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid fen: %v", err)
	}

	moves, err := s.store.OpeningExplorer(ctx, fen, int(req.GetLimit()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "opening explorer: %v", err)
	}
	out := make([]*gamev1.OpeningMove, 0, len(moves))
	var total int64
	for _, m := range moves {
		total += m.Total
		out = append(out, &gamev1.OpeningMove{
			Uci:       m.UCI,
			San:       m.SAN,
			WhiteWins: m.WhiteWins,
			BlackWins: m.BlackWins,
			Draws:     m.Draws,
			Total:     m.Total,
		})
	}
	return &gamev1.OpeningExplorerResponse{Moves: out, TotalGames: total}, nil
}

// GetAnalysis returns a game's report.
//
// NOT_FOUND simply means the worker has not got to it yet: analysis is async, so
// "no report" is an ordinary state a client should render as "analysing…", not
// an error.
func (s *Server) GetAnalysis(ctx context.Context, req *gamev1.GetAnalysisRequest) (*gamev1.GetAnalysisResponse, error) {
	if req.GetGameId() == "" {
		return nil, status.Error(codes.InvalidArgument, "game_id is required")
	}
	r, err := s.store.GetAnalysis(ctx, req.GetGameId())
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "this game has not been analysed yet")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load analysis: %v", err)
	}

	moves := make([]*gamev1.MoveVerdict, 0, len(r.Moves))
	for _, m := range r.Moves {
		moves = append(moves, &gamev1.MoveVerdict{
			Ply:           uint32(m.Ply),
			Uci:           m.UCI,
			BestUci:       m.BestUCI,
			EvalBeforeCp:  int32(m.EvalBeforeCP),
			EvalAfterCp:   int32(m.EvalAfterCP),
			CentipawnLoss: int32(m.CentipawnLoss),
			Quality:       m.Quality,
			MatchedEngine: m.MatchedEngine,
		})
	}

	return &gamev1.GetAnalysisResponse{
		GameId:     r.GameID,
		Depth:      uint32(r.Depth),
		White:      toSideAnalysis(r.White),
		Black:      toSideAnalysis(r.Black),
		Moves:      moves,
		NoveltyFen: r.NoveltyFEN,
		NoveltyPly: uint32(r.NoveltyPly),
		HasNovelty: r.NoveltyFEN != "",
		AnalyzedAt: timestamppb.New(r.AnalyzedAt),
	}, nil
}

func toSideAnalysis(s store.SideReport) *gamev1.SideAnalysis {
	return &gamev1.SideAnalysis{
		Accuracy:     s.Accuracy,
		Acpl:         s.ACPL,
		MatchRate:    s.MatchRate,
		Blunders:     uint32(s.Blunders),
		Mistakes:     uint32(s.Mistakes),
		Inaccuracies: uint32(s.Inaccuracies),
	}
}

// GamePgnUrl returns a presigned download URL for a game's archived PGN.
//
// Presigned so the client downloads straight from object storage rather than
// streaming a file through the gateway. Empty url (not an error) means the game
// has not been archived — it may still be in progress, or archival is disabled.
func (s *Server) GamePgnUrl(ctx context.Context, req *gamev1.GamePgnUrlRequest) (*gamev1.GamePgnUrlResponse, error) {
	if req.GetGameId() == "" {
		return nil, status.Error(codes.InvalidArgument, "game_id is required")
	}
	if !s.objects.Enabled() {
		return &gamev1.GamePgnUrlResponse{}, nil
	}
	key := req.GetGameId() + ".pgn"
	exists, err := s.objects.Exists(ctx, objstore.BucketPGN, key)
	if err != nil || !exists {
		return &gamev1.GamePgnUrlResponse{}, nil
	}
	url, err := s.objects.PresignedGet(ctx, objstore.BucketPGN, key,
		15*time.Minute, req.GetGameId()+".pgn")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "presign pgn: %v", err)
	}
	return &gamev1.GamePgnUrlResponse{Url: url}, nil
}
