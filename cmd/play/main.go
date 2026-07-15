// Command play is a small CLI that plays a game against the engine through the
// game-service gRPC API. It reads UCI moves from stdin and prints the board and
// the engine's replies. It exists to demo the Q1 vertical slice without a web UI.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
)

func main() {
	addr := config.Getenv("ACG_GAME_ADDR", "localhost:50051")
	depth := config.GetenvInt("ACG_ENGINE_DEPTH", 10)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := gamev1.NewGameServiceClient(conn)

	ctx := context.Background()
	created, err := client.CreateGame(ctx, &gamev1.CreateGameRequest{EngineDepth: uint32(depth)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "create game:", err)
		os.Exit(1)
	}
	game := created.GetGame()

	fmt.Printf("New game %s — you are White, engine depth %d.\n", game.GetId(), depth)
	fmt.Println("Enter moves in UCI notation (e.g. e2e4, e7e8q). Type 'quit' to exit.")
	printBoard(game.GetFen())

	reader := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("your move> ")
		if !reader.Scan() {
			break
		}
		move := strings.TrimSpace(reader.Text())
		if move == "" {
			continue
		}
		if move == "quit" || move == "exit" {
			fmt.Println("bye.")
			return
		}

		reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		resp, err := client.SubmitMove(reqCtx, &gamev1.SubmitMoveRequest{GameId: game.GetId(), Uci: move})
		cancel()
		if err != nil {
			fmt.Println("  ✗", err)
			continue
		}
		game = resp.GetGame()

		if reply := lastEngineMove(game); reply != "" {
			fmt.Println("engine plays:", reply)
		}
		printBoard(game.GetFen())

		if game.GetStatus() != gamev1.Status_STATUS_IN_PROGRESS {
			fmt.Printf("Game over: %s (%s)\n", game.GetStatus(), game.GetEndReason())
			return
		}
	}
}

// lastEngineMove returns the UCI of the final move when it was the engine's
// (i.e. an even ply, since White moves on odd plies).
func lastEngineMove(g *gamev1.Game) string {
	moves := g.GetMoves()
	if len(moves) == 0 {
		return ""
	}
	last := moves[len(moves)-1]
	if last.GetPly()%2 == 0 {
		return last.GetUci()
	}
	return ""
}

// printBoard renders an ASCII board from a FEN string.
func printBoard(fen string) {
	b, err := chess.ParseFEN(fen)
	if err != nil {
		fmt.Println("(could not render board:", err, ")")
		return
	}
	fmt.Println()
	for rank := 7; rank >= 0; rank-- {
		fmt.Printf(" %d ", rank+1)
		for file := 0; file < 8; file++ {
			p := b.PieceAt(rank*8 + file)
			fmt.Printf(" %s", pieceRune(p))
		}
		fmt.Println()
	}
	fmt.Println("    a b c d e f g h")
	fmt.Println()
}

// pieceRune returns a single-character representation of a piece.
func pieceRune(p chess.Piece) string {
	if p.IsEmpty() {
		return "."
	}
	letters := map[chess.PieceType]string{
		chess.Pawn: "p", chess.Knight: "n", chess.Bishop: "b",
		chess.Rook: "r", chess.Queen: "q", chess.King: "k",
	}
	s := letters[p.Type()]
	if p.Color() == chess.White {
		s = strings.ToUpper(s)
	}
	return s
}
