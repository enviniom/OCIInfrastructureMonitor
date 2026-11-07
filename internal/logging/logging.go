// Package logging provides the small leveled logger used by the commands.
// Messages go to a single writer (normally stderr) and carry a UTC timestamp,
// the level and the formatted message. Credentials must never be passed to it:
// the Telegram client redacts the bot token from its errors before they reach
// the logger.
package logging

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Level is the minimum severity a logger writes.
type Level int

const (
	// LevelDebug writes every message.
	LevelDebug Level = iota
	// LevelInfo writes info, warnings and errors.
	LevelInfo
	// LevelWarn writes warnings and errors.
	LevelWarn
	// LevelError writes only errors.
	LevelError
)

// ParseLevel maps the logging.level configuration values to a Level.
func ParseLevel(name string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return LevelDebug, nil
	case "info":
		return LevelInfo, nil
	case "warn":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q, want debug, info, warn or error", name)
	}
}

// Logger writes timestamped, leveled lines to its writer.
type Logger struct {
	mu    sync.Mutex
	w     io.Writer
	level Level
	now   func() time.Time
}

// New returns a logger writing to w at the given level. A nil writer discards
// every message.
func New(level Level, w io.Writer) *Logger {
	return &Logger{w: w, level: level, now: time.Now}
}

// NewFromName is New with the configuration string.
func NewFromName(name string, w io.Writer) (*Logger, error) {
	level, err := ParseLevel(name)
	if err != nil {
		return nil, err
	}
	return New(level, w), nil
}

// Level returns the configured minimum level.
func (l *Logger) Level() Level {
	if l == nil {
		return LevelInfo
	}
	return l.level
}

// Debugf writes a debug message.
func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, "debug", format, args...) }

// Infof writes an informational message.
func (l *Logger) Infof(format string, args ...any) { l.logf(LevelInfo, "info", format, args...) }

// Warnf writes a warning.
func (l *Logger) Warnf(format string, args ...any) { l.logf(LevelWarn, "warn", format, args...) }

// Errorf writes an error.
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, "error", format, args...) }

func (l *Logger) logf(level Level, label, format string, args ...any) {
	if l == nil || l.w == nil || level < l.level {
		return
	}
	message := fmt.Sprintf(format, args...)
	message = strings.TrimRight(message, "\n")

	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %-5s %s\n", l.now().UTC().Format(time.RFC3339), label, message)
}
