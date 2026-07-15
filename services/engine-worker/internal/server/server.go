// Package server implements the EngineService gRPC API on top of a UCI engine.
package server

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	enginev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/engine/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/engine-worker/internal/uci"
)

// Server adapts a uci.Engine to the EngineService gRPC interface.
type Server struct {
	enginev1.UnimplementedEngineServiceServer
	engine *uci.Engine
}

// New returns a Server backed by the given engine.
func New(engine *uci.Engine) *Server {
	return &Server{engine: engine}
}

// Analyze evaluates the requested position and returns the engine's best move.
func (s *Server) Analyze(ctx context.Context, req *enginev1.AnalyzeRequest) (*enginev1.AnalyzeResponse, error) {
	if req.GetFen() == "" {
		return nil, status.Error(codes.InvalidArgument, "fen is required")
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
