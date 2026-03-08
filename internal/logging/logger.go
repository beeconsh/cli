package logging

import (
	"context"
	"log/slog"
	"os"
)

// Logger is the package-level debug logger. Defaults to a no-op.
// Call Enable() to activate debug logging to stderr.
var Logger *slog.Logger = slog.New(discardHandler{})

// Enable activates debug-level logging to stderr.
func Enable() {
	Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

// discardHandler is a no-op slog.Handler.
type discardHandler struct{}

func (discardHandler) Enabled(_ context.Context, _ slog.Level) bool  { return false }
func (discardHandler) Handle(_ context.Context, _ slog.Record) error { return nil }
func (discardHandler) WithAttrs(_ []slog.Attr) slog.Handler          { return discardHandler{} }
func (discardHandler) WithGroup(_ string) slog.Handler               { return discardHandler{} }
