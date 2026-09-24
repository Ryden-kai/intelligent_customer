// Package log wraps zerolog with a few opinionated defaults so the rest of
// the codebase can use a single configured instance everywhere. Development
// mode enables DEBUG level + human-friendly colour output; production uses
// structured JSON and the level is read from LOG_LEVEL.
package log

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

type ctxKey struct{ name string }

var requestIDKey = ctxKey{"request_id"}

// New creates the root logger. level is "debug" | "info" | "warn" | "error";
// pretty enables human-friendly console output (development only).
func New(level string, pretty bool) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.MessageFieldName = "msg"
	zerolog.LevelFieldName = "level"
	zerolog.TimestampFieldName = "ts"
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		short := file
		if i := strings.LastIndex(file, "/"); i >= 0 {
			if j := strings.LastIndex(file[:i], "/"); j >= 0 {
				short = file[j+1:]
			}
		}
		return short + ":" + itoa(line)
	}

	var lvl zerolog.Level
	switch strings.ToLower(level) {
	case "trace":
		lvl = zerolog.TraceLevel
	case "debug":
		lvl = zerolog.DebugLevel
	case "warn", "warning":
		lvl = zerolog.WarnLevel
	case "error":
		lvl = zerolog.ErrorLevel
	default:
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	var w io.Writer = os.Stdout
	if pretty {
		w = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339Nano, NoColor: false}
	}

	root := zerolog.New(w).With().
		Timestamp().
		Caller().
		Str("svc", "intelligent_customer").
		Logger()
	return root
}

// WithRequestID returns a context carrying the supplied request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom extracts the request id; empty string when absent.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// With attaches the request id (when present) to the supplied logger so
// every log line in the request scope carries the same id. The returned
// pointer is addressable so callers can chain Warn/Info/Error directly.
func With(ctx context.Context, base zerolog.Logger) *zerolog.Logger {
	if id := RequestIDFrom(ctx); id != "" {
		l := base.With().Str("req_id", id).Logger()
		return &l
	}
	l := base
	return &l
}

func itoa(n int) string {
	// Tiny helper to avoid pulling strconv for one call.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}