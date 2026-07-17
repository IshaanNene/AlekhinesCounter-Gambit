// Package server implements the GameService gRPC API: it validates moves with
// pkg/chess, persists them, and produces engine replies for games played
// against the engine.
package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/engine"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/kafkax"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/objstore"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/telemetry"
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/game-service/internal/session"
)

// engineMoveTimeout bounds how long we wait for an engine reply.
const engineMoveTimeout = 30 * time.Second

// sessionTimeout bounds calls to the session-manager. Session state is live but
// not authoritative, so we fail fast rather than stall a move.
const sessionTimeout = 5 * time.Second

// Server implements gamev1.GameServiceServer.
type Server struct {
	gamev1.UnimplementedGameServiceServer
	store   *store.Store
	engine  *engine.Client
	session *session.Client
	// events queues finished games for analysis. May be disabled.
	events *kafkax.Producer
	// objects archives finished PGNs. May be disabled.
	objects       *objstore.Store
	analysisDepth int
	metrics       *telemetry.Metrics
	log           *slog.Logger
}

// New builds a Server. sess and events may be disabled clients, in which case
// live sessions and background analysis are skipped respectively.
func New(st *store.Store, eng *engine.Client, sess *session.Client, events *kafkax.Producer, objects *objstore.Store, analysisDepth int, metrics *telemetry.Metrics, log *slog.Logger) *Server {
	if analysisDepth <= 0 {
		analysisDepth = 14
	}
	return &Server{
		store: st, engine: eng, session: sess,
		events: events, objects: objects, analysisDepth: analysisDepth,
		metrics: metrics, log: log,
	}
}

// Metric helpers. All nil-safe so the server runs without metrics.

// gameCreated bumps the in-progress gauge.
func (s *Server) gameCreated() {
	if s.metrics != nil {
		s.metrics.GamesActive.Inc()
	}
}

// gameEnded closes out a finished game: it leaves the in-progress gauge and
// counts the result. Called from every terminal path (checkmate, resignation).
func (s *Server) gameEnded(result chess.Result) {
	if s.metrics == nil {
		return
	}
	s.metrics.GamesActive.Dec()
	s.metrics.GamesFinished.WithLabelValues(resultToDB(result)).Inc()
}

// recordMove feeds the move gauge and, when the move ended the game, the
// finished-game counters.
func (s *Server) recordMove(ended bool, result chess.Result) {
	if s.metrics == nil {
		return
	}
	s.metrics.MovesTotal.Inc()
	if ended {
		s.gameEnded(result)
	}
}

// CreateGame starts a new game. When white_id is empty a guest user is created;
// when black_id is empty the game is played against the engine.
func (s *Server) CreateGame(ctx context.Context, req *gamev1.CreateGameRequest) (*gamev1.CreateGameResponse, error) {
	whiteID := req.GetWhiteId()
	if whiteID == "" {
		var err error
		whiteID, err = s.store.CreateGuestUser(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "create guest: %v", err)
		}
	}

	g, err := s.store.CreateGame(ctx, store.CreateGameParams{
		WhiteID:     whiteID,
		BlackID:     req.GetBlackId(),
		VsEngine:    req.GetVsEngine(),
		EngineDepth: int(req.GetEngineDepth()),
		StartFEN:    chess.StartFEN,
		Rated:       req.GetRated(),
		InitialMs:   req.GetInitialMs(),
		IncrementMs: req.GetIncrementMs(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create game: %v", err)
	}

	// Live sessions need both seats filled, so an open game waits for JoinGame.
	// Engine games never need one.
	if !g.VsEngine && !g.AwaitingOpponent && s.session.Enabled() {
		sctx, cancel := context.WithTimeout(ctx, sessionTimeout)
		defer cancel()
		if err := s.session.Create(sctx, session.CreateParams{
			GameID:      g.ID,
			WhiteID:     g.WhiteID,
			BlackID:     g.BlackID,
			InitialMs:   req.GetInitialMs(),
			IncrementMs: req.GetIncrementMs(),
		}); err != nil {
			return nil, status.Errorf(codes.Internal, "create session: %v", err)
		}
	}
	s.gameCreated()
	return &gamev1.CreateGameResponse{Game: toProtoGame(g, nil)}, nil
}

// SubmitMove validates and applies a move, then (for engine games) applies the
// engine's reply, and returns the updated game.
func (s *Server) SubmitMove(ctx context.Context, req *gamev1.SubmitMoveRequest) (*gamev1.SubmitMoveResponse, error) {
	if req.GetGameId() == "" || req.GetUci() == "" {
		return nil, status.Error(codes.InvalidArgument, "game_id and uci are required")
	}

	g, moves, err := s.store.GetGame(ctx, req.GetGameId())
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "game not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load game: %v", err)
	}
	if g.Status != "IN_PROGRESS" {
		return nil, status.Error(codes.FailedPrecondition, "game is already over")
	}

	// Position history (for threefold): start position + every move so far.
	history := buildHistory(moves)

	// Apply the player's move.
	board, err := chess.ParseFEN(g.FEN)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "corrupt stored fen: %v", err)
	}

	// For human-vs-human games, only the side to move may move.
	if !g.VsEngine {
		if err := authorizeMover(g, board.Turn, req.GetPlayerId()); err != nil {
			return nil, err
		}
	}

	after, err := board.ApplyUCI(req.GetUci())
	if err != nil {
		// The chess package's error already reads "illegal move: e2e5".
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	history = append(history, after.FEN())

	ply := len(moves) + 1
	result, reason := after.OutcomeWithHistory(history)
	ended := result != chess.InProgress
	if err := s.store.AppendMove(ctx, g.ID,
		store.Move{Ply: ply, UCI: req.GetUci(), FENAfter: after.FEN()},
		resultToDB(result), reasonToDB(reason), ended); err != nil {
		return nil, status.Errorf(codes.Internal, "persist move: %v", err)
	}

	// Tell the live session a move landed so it can switch the turn and apply the
	// clock. The game-service is authoritative for legality, so a session error
	// must not fail an already-persisted move — log and carry on.
	if !g.VsEngine && s.session.Enabled() {
		if ended {
			// Chess-level termination: close the session so its clocks stop.
			s.endSession(ctx, g.ID, result, reason)
		} else {
			s.notifySession(ctx, g.ID, req.GetPlayerId())
		}
	}
	s.recordMove(ended, result)
	if ended {
		s.applyRatings(ctx, g.ID, result)
		s.requestAnalysis(ctx, g.ID)
		s.archivePGN(ctx, g.ID)
		s.ingestOpening(ctx, g.ID, resultToDB(result))
	}

	// Engine reply, when applicable and the game is still going.
	if !ended && g.VsEngine {
		if err := s.playEngineReply(ctx, g, after, history, ply+1); err != nil {
			return nil, err
		}
	}

	// Return the freshly persisted state.
	updated, updatedMoves, err := s.store.GetGame(ctx, g.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reload game: %v", err)
	}
	return &gamev1.SubmitMoveResponse{Game: toProtoGame(updated, updatedMoves)}, nil
}

// playEngineReply asks the engine for a move in position `after`, applies it, and
// persists it.
func (s *Server) playEngineReply(ctx context.Context, g *store.Game, after *chess.Board, history []string, ply int) error {
	ectx, cancel := context.WithTimeout(ctx, engineMoveTimeout)
	defer cancel()

	uci, err := s.engine.BestMove(ectx, after.FEN(), g.EngineDepth)
	if err != nil {
		return status.Errorf(codes.Internal, "engine reply: %v", err)
	}
	engineAfter, err := after.ApplyUCI(uci)
	if err != nil {
		// The engine should never return an illegal move; surface it clearly.
		s.log.Error("engine returned illegal move", "move", uci, "fen", after.FEN())
		return status.Errorf(codes.Internal, "engine returned illegal move %q: %v", uci, err)
	}
	history = append(history, engineAfter.FEN())
	result, reason := engineAfter.OutcomeWithHistory(history)
	ended := result != chess.InProgress
	if err := s.store.AppendMove(ctx, g.ID,
		store.Move{Ply: ply, UCI: uci, FENAfter: engineAfter.FEN()},
		resultToDB(result), reasonToDB(reason), ended); err != nil {
		return status.Errorf(codes.Internal, "persist engine move: %v", err)
	}
	return nil
}

// LegalMoves returns every legal move in the game's current position, so
// clients can highlight destinations and screen premoves without shipping a
// second move generator that could disagree with this one.
func (s *Server) LegalMoves(ctx context.Context, req *gamev1.LegalMovesRequest) (*gamev1.LegalMovesResponse, error) {
	if req.GetGameId() == "" {
		return nil, status.Error(codes.InvalidArgument, "game_id is required")
	}
	g, _, err := s.store.GetGame(ctx, req.GetGameId())
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "game not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load game: %v", err)
	}
	board, err := chess.ParseFEN(g.FEN)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "corrupt stored fen: %v", err)
	}

	moves := board.LegalMoves()
	out := make([]string, 0, len(moves))
	for _, m := range moves {
		out = append(out, m.String())
	}
	return &gamev1.LegalMovesResponse{Uci: out}, nil
}

// Resign ends a game in the opponent's favour. Either participant may resign at
// any point while the game is in progress; it is not turn-dependent.
func (s *Server) Resign(ctx context.Context, req *gamev1.ResignRequest) (*gamev1.ResignResponse, error) {
	if req.GetGameId() == "" || req.GetPlayerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "game_id and player_id are required")
	}

	g, _, err := s.store.GetGame(ctx, req.GetGameId())
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "game not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load game: %v", err)
	}

	// Identify the resigning side; the opponent wins.
	var winner chess.Result
	switch req.GetPlayerId() {
	case g.WhiteID:
		winner = chess.BlackWins
	case g.BlackID:
		winner = chess.WhiteWins
	default:
		return nil, status.Error(codes.PermissionDenied, "player is not in this game")
	}

	err = s.store.EndGame(ctx, g.ID, resultToDB(winner), "RESIGNATION")
	if errors.Is(err, store.ErrNotInProgress) {
		return nil, status.Error(codes.FailedPrecondition, "game is already over")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "end game: %v", err)
	}
	s.gameEnded(winner)

	// Mirror the result into the live session so its clocks stop.
	if !g.VsEngine && s.session.Enabled() {
		sctx, cancel := context.WithTimeout(ctx, sessionTimeout)
		defer cancel()
		if _, err := s.session.Resign(sctx, g.ID, req.GetPlayerId()); err != nil {
			s.log.Warn("session resign failed", "game_id", g.ID, "error", err)
		}
	}
	s.applyRatings(ctx, g.ID, winner)
	s.requestAnalysis(ctx, g.ID)
	s.archivePGN(ctx, g.ID)
	s.ingestOpening(ctx, g.ID, resultToDB(winner))

	updated, moves, err := s.store.GetGame(ctx, g.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reload game: %v", err)
	}
	return &gamev1.ResignResponse{Game: toProtoGame(updated, moves)}, nil
}

// JoinGame claims a game's open Black seat and starts its live session.
func (s *Server) JoinGame(ctx context.Context, req *gamev1.JoinGameRequest) (*gamev1.JoinGameResponse, error) {
	if req.GetGameId() == "" || req.GetPlayerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "game_id and player_id are required")
	}

	g, err := s.store.JoinGame(ctx, req.GetGameId(), req.GetPlayerId())
	if errors.Is(err, store.ErrSeatUnavailable) {
		return nil, status.Error(codes.FailedPrecondition,
			"this game is not open to join — it may already have an opponent")
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "game not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "join game: %v", err)
	}

	// Both seats are filled, so the live session can start now.
	if s.session.Enabled() {
		sctx, cancel := context.WithTimeout(ctx, sessionTimeout)
		defer cancel()
		if err := s.session.Create(sctx, session.CreateParams{
			GameID:      g.ID,
			WhiteID:     g.WhiteID,
			BlackID:     g.BlackID,
			InitialMs:   g.InitialMs,
			IncrementMs: g.IncrementMs,
		}); err != nil {
			s.log.Warn("session create on join failed", "game_id", g.ID, "error", err)
		}
	}

	_, moves, err := s.store.GetGame(ctx, g.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reload game: %v", err)
	}
	return &gamev1.JoinGameResponse{Game: toProtoGame(g, moves)}, nil
}

// authorizeMover checks that playerID owns the side to move.
func authorizeMover(g *store.Game, turn chess.Color, playerID string) error {
	if playerID == "" {
		return status.Error(codes.InvalidArgument, "player_id is required for human-vs-human games")
	}
	expected := g.WhiteID
	if turn == chess.Black {
		expected = g.BlackID
	}
	if playerID != expected {
		return status.Errorf(codes.PermissionDenied, "not %s's turn", playerID)
	}
	return nil
}

// notifySession informs the session-manager of a completed move. Failures are
// logged, never fatal: the move is already durably persisted here.
func (s *Server) notifySession(ctx context.Context, gameID, playerID string) {
	sctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	snap, err := s.session.MoveMade(sctx, gameID, playerID)
	if err != nil {
		s.log.Warn("session move notification failed", "game_id", gameID, "error", err)
		return
	}
	// A non-empty error means the session disagreed with us (e.g. it thinks it is
	// the other side's turn) — a desync worth surfacing.
	if snap != nil && snap.GetError() != "" {
		s.log.Warn("session rejected move notification",
			"game_id", gameID, "player_id", playerID, "session_error", snap.GetError())
	}
}

// endSession closes the live session after a chess-level termination. Like
// notifySession, failures are logged rather than fatal.
func (s *Server) endSession(ctx context.Context, gameID string, result chess.Result, reason chess.EndReason) {
	sctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	var winner session.Winner
	switch result {
	case chess.WhiteWins:
		winner = session.WinnerWhite
	case chess.BlackWins:
		winner = session.WinnerBlack
	default:
		winner = session.WinnerNone
	}
	if err := s.session.End(sctx, gameID, winner, reasonToSession(reason)); err != nil {
		s.log.Warn("session end notification failed", "game_id", gameID, "error", err)
	}
}

// GetGame returns a game and its moves.
func (s *Server) GetGame(ctx context.Context, req *gamev1.GetGameRequest) (*gamev1.GetGameResponse, error) {
	if req.GetGameId() == "" {
		return nil, status.Error(codes.InvalidArgument, "game_id is required")
	}
	g, moves, err := s.store.GetGame(ctx, req.GetGameId())
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "game not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load game: %v", err)
	}
	return &gamev1.GetGameResponse{Game: toProtoGame(g, moves)}, nil
}

// buildHistory reconstructs the list of positions reached, starting from the
// standard initial position (Q1 games always start there).
func buildHistory(moves []store.Move) []string {
	history := make([]string, 0, len(moves)+2)
	history = append(history, chess.StartFEN)
	for _, m := range moves {
		history = append(history, m.FENAfter)
	}
	return history
}

// toProtoGame converts a stored game (+moves) to the proto representation.
func toProtoGame(g *store.Game, moves []store.Move) *gamev1.Game {
	pg := &gamev1.Game{
		Id:               g.ID,
		Fen:              g.FEN,
		Status:           statusFromDB(g.Status),
		EndReason:        endReasonFromDB(g.EndReason),
		VsEngine:         g.VsEngine,
		AwaitingOpponent: g.AwaitingOpponent,
		Rated:            g.Rated,
		WhiteId:          g.WhiteID,
		BlackId:          g.BlackID,
		StartedAt:        timestamppb.New(g.StartedAt),
	}
	if g.EndedAt != nil {
		pg.EndedAt = timestamppb.New(*g.EndedAt)
	}
	for _, m := range moves {
		pg.Moves = append(pg.Moves, &gamev1.Move{
			Ply:      uint32(m.Ply),
			Uci:      m.UCI,
			FenAfter: m.FENAfter,
		})
	}
	return pg
}
