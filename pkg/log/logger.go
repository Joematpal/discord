// Package log defines the Logger interface used throughout the discord packages.
package log

// Logger is a minimal structured logging interface.
// *slog.Logger satisfies this out of the box.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Nop returns a Logger that discards all output.
func Nop() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}
