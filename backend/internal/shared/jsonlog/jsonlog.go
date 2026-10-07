// Package jsonlog builds the application's structured, leveled JSON logger.
// It is a thin wrapper over log/slog: the returned value is a plain
// *slog.Logger, so call sites use the standard slog API and the logger can
// be handed to anything that accepts one (e.g. http.Server.ErrorLog via
// slog.NewLogLogger).
package jsonlog

import (
	"io"
	"log/slog"
)

// New returns a logger that writes one JSON object per line to out,
// discarding entries below minLevel.
func New(out io.Writer, minLevel slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: minLevel}))
}

// LevelForEnv returns the minimum log level for an environment name:
// debug in development, info everywhere else.
func LevelForEnv(env string) slog.Level {
	if env == "development" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
