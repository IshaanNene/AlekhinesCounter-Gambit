// Package server implements the EngineService gRPC API on top of a UCI engine.
package server

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/openingbook"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/telemetry"
	enginev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/engine/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/engine-worker/internal/uci"
)

// Server adapts a uci.Engine to the EngineService gRPC interface.
type Server struct {
	enginev1.UnimplementedEngineServiceServer
	engine  *uci.Engine
	book    *openingbook.Book
	metrics *telemetry.Metrics
}

// New returns a Server backed by the given engine and opening book. The book may
// be nil (or empty), in which case every request is searched. metrics may be nil.
func New(engine *uci.Engine, book *openingbook.Book, metrics *telemetry.Metrics) *Server {
	return &Server{engine: engine, book: book, metrics: metrics}
}

// Analyze evaluates the requested position and returns the engine's best move.
//
// When the caller opts in with use_book (engine play, not analysis) and the
// position is in the opening book, the book move is returned without searching —
// giving fast, varied openings. Analysis never sets the flag, so it always gets
// a true evaluation.
func (s *Server) Analyze(ctx context.Context, req *enginev1.AnalyzeRequest) (*enginev1.AnalyzeResponse, error) {
	if req.GetFen() == "" {
		return nil, status.Error(codes.InvalidArgument, "fen is required")
	}

	if req.GetUseBook() {
		if move, ok := s.book.Move(req.GetFen()); ok {
			return &enginev1.AnalyzeResponse{Bestmove: move, FromBook: true}, nil
		}
	}

	// A real search — a book hit above is not an "analysis", so it is not counted.
	if s.metrics != nil {
		s.metrics.EngineAnalyses.Inc()
	}
	res, err := s.engine.Analyze(ctx, req.GetFen(), req.GetDepth(), req.GetMovetimeMs())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("analyze: %v", err))
	}

	return &enginev1.AnalyzeResponse{
		Bestmove: res.BestMove,
		ScoreCp:  res.ScoreCP,
		Mate:     res.Mate,
		MateIn:   res.MateIn,
		Depth:    res.Depth,
		Pv:       res.PV,
	}, nil
}
