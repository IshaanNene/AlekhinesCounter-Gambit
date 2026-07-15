// Package uci drives a UCI-speaking chess engine (e.g. Stockfish) as a child
// process. An Engine is safe for concurrent use: each analysis holds an internal
// lock so only one search runs on the underlying process at a time.
package uci

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result is the outcome of a search.
type Result struct {
	BestMove string   // UCI move, or "(none)" when the position is terminal
	ScoreCP  int32    // centipawns, side-to-move relative (valid when Mate is false)
	Mate     bool     // true when Score is a forced mate
	MateIn   int32    // moves to mate when Mate is true (sign = side to move)
	Depth    uint32   // depth reached
	PV       []string // principal variation in UCI
}

// Engine wraps a running UCI engine process. A single long-lived goroutine reads
// the engine's stdout into the lines channel; all consumers read from there so
// that no two readers ever touch the underlying stream concurrently.
type Engine struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   chan string
	readErr error
}

// New starts the engine at path and completes the UCI handshake.
func New(path string) (*Engine, error) {
	cmd := exec.Command(path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start engine %q: %w", path, err)
	}

	e := &Engine{cmd: cmd, stdin: stdin, lines: make(chan string, 128)}
	go e.readLoop(bufio.NewReader(stdout))

	if err := e.handshake(); err != nil {
		_ = e.Close()
		return nil, err
	}
	return e, nil
}

// readLoop is the sole reader of the engine's stdout. It publishes each line
// (newline-stripped) to e.lines and closes the channel on EOF/error.
func (e *Engine) readLoop(r *bufio.Reader) {
	defer close(e.lines)
	for {
		line, err := r.ReadString('\n')
		if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
			e.lines <- trimmed
		}
		if err != nil {
			e.readErr = err
			return
		}
	}
}

// send writes a single UCI command line.
func (e *Engine) send(cmd string) error {
	_, err := io.WriteString(e.stdin, cmd+"\n")
	return err
}

// handshake performs "uci"/"uciok" then "isready"/"readyok".
func (e *Engine) handshake() error {
	if err := e.send("uci"); err != nil {
		return fmt.Errorf("send uci: %w", err)
	}
	if err := e.waitFor("uciok"); err != nil {
		return err
	}
	return e.ready()
}

// ready issues "isready" and waits for "readyok".
func (e *Engine) ready() error {
	if err := e.send("isready"); err != nil {
		return fmt.Errorf("send isready: %w", err)
	}
	return e.waitFor("readyok")
}

// waitFor reads published lines until one begins with token.
func (e *Engine) waitFor(token string) error {
	for line := range e.lines {
		if strings.HasPrefix(line, token) {
			return nil
		}
	}
	return fmt.Errorf("engine closed while waiting for %q: %w", token, e.readErr)
}

// Analyze evaluates fen. If movetimeMS > 0 the search is time-limited; otherwise
// depth is used (defaulting to 12 when both are zero). The context bounds the
// total wait; on cancellation the engine is told to stop and the search is
// drained so the stream stays clean for the next call.
func (e *Engine) Analyze(ctx context.Context, fen string, depth, movetimeMS uint32) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ready(); err != nil {
		return Result{}, err
	}
	if err := e.send("position fen " + fen); err != nil {
		return Result{}, fmt.Errorf("send position: %w", err)
	}

	var goCmd string
	switch {
	case movetimeMS > 0:
		goCmd = "go movetime " + strconv.FormatUint(uint64(movetimeMS), 10)
	default:
		if depth == 0 {
			depth = 12
		}
		goCmd = "go depth " + strconv.FormatUint(uint64(depth), 10)
	}
	if err := e.send(goCmd); err != nil {
		return Result{}, fmt.Errorf("send go: %w", err)
	}

	return e.readSearch(ctx)
}

// readSearch consumes "info" lines (tracking the latest score/pv/depth) until
// "bestmove". On context cancellation it sends "stop" once and keeps draining
// until "bestmove" so the engine and stream are left in a clean state.
func (e *Engine) readSearch(ctx context.Context) (Result, error) {
	var res Result
	stopped := false
	for {
		select {
		case <-ctx.Done():
			if !stopped {
				_ = e.send("stop")
				stopped = true
			}
			line, ok := <-e.lines
			if !ok {
				return Result{}, fmt.Errorf("engine closed during search: %w", e.readErr)
			}
			if strings.HasPrefix(line, "bestmove") {
				setBestMove(line, &res)
				return res, ctx.Err()
			}
		case line, ok := <-e.lines:
			if !ok {
				return Result{}, fmt.Errorf("engine closed during search: %w", e.readErr)
			}
			switch {
			case strings.HasPrefix(line, "info "):
				parseInfo(line, &res)
			case strings.HasPrefix(line, "bestmove"):
				setBestMove(line, &res)
				return res, nil
			}
		}
	}
}

// setBestMove extracts the move from a "bestmove <move> [ponder <move>]" line.
func setBestMove(line string, res *Result) {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		res.BestMove = fields[1]
	}
}

// parseInfo updates res from a single "info ..." line.
func parseInfo(line string, res *Result) {
	fields := strings.Fields(line)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "depth":
			if i+1 < len(fields) {
				if d, err := strconv.Atoi(fields[i+1]); err == nil {
					res.Depth = uint32(d)
				}
			}
		case "score":
			if i+2 < len(fields) {
				kind, valStr := fields[i+1], fields[i+2]
				if val, err := strconv.Atoi(valStr); err == nil {
					if kind == "mate" {
						res.Mate = true
						res.MateIn = int32(val)
						res.ScoreCP = 0
					} else { // "cp"
						res.Mate = false
						res.ScoreCP = int32(val)
						res.MateIn = 0
					}
				}
			}
		case "pv":
			res.PV = append([]string(nil), fields[i+1:]...)
			return // pv is always last
		}
	}
}

// Close terminates the engine process.
func (e *Engine) Close() error {
	_ = e.send("quit")
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- e.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = e.cmd.Process.Kill()
		<-done
	}
	return nil
}
