// Package config holds small helpers for 12-factor configuration: all runtime
// configuration comes from ACG_-prefixed environment variables with sane
// localhost defaults.
package config

import (
	"log/slog"
	"os"
	"strconv"
)

// Getenv returns the value of key, or def when unset or empty.
func Getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// GetenvInt returns the integer value of key, or def when unset or unparseable.
func GetenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// NewLogger returns a structured JSON logger writing to stderr.
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
