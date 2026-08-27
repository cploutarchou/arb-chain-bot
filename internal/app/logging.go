package app

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// sensitiveKeys are attribute names whose values are always redacted in
// logs, whatever the call site does. Extend the list, never bypass it.
var sensitiveKeys = map[string]struct{}{
	"password": {}, "secret": {}, "token": {}, "api_key": {}, "apikey": {},
	"authorization": {}, "cookie": {}, "session": {}, "passphrase": {}, "dsn": {},
}

// logLevel is the process-wide level (settings-expansion §4.3 D6): the
// process has exactly one logger, and threading a LevelVar through six
// cmd/ entry points and BuildComponents to reach one subscriber callback
// is more plumbing than the thing it configures. slog.LevelVar is
// atomic, so SetLogLevel is race-free against concurrent logging.
var logLevel slog.LevelVar

// NewLogger builds the process logger: structured JSON on stdout with the
// configured level and unconditional redaction of sensitive attributes.
// The level installed here is the boot seed; platform.log_level (hot)
// changes it later through SetLogLevel.
func NewLogger(level string) *slog.Logger {
	lvl, err := ParseLogLevel(level)
	if err != nil {
		lvl = slog.LevelInfo
	}
	logLevel.Set(lvl)
	return NewLoggerWithLevel(&logLevel)
}

// NewLoggerWithLevel builds the same handler over a caller-owned
// leveler (tests use it to capture output without touching the
// process-wide level).
func NewLoggerWithLevel(lvl slog.Leveler) *slog.Logger {
	return slog.New(newHandler(os.Stdout, lvl))
}

func newHandler(w io.Writer, lvl slog.Leveler) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, sensitive := sensitiveKeys[strings.ToLower(a.Key)]; sensitive {
				a.Value = slog.StringValue("***")
			}
			return a
		},
	})
}

// ParseLogLevel maps debug|info|warn|error (case-insensitive) to a level.
func ParseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("app: unknown log level %q", level)
}

// SetLogLevel changes the process log level at runtime (hot). Called by
// the platform-settings subscriber on every swap.
func SetLogLevel(level string) error {
	lvl, err := ParseLogLevel(level)
	if err != nil {
		return err
	}
	logLevel.Set(lvl)
	return nil
}

// CurrentLogLevel reports the level in force, in the settings enum.
func CurrentLogLevel() string {
	switch logLevel.Level() {
	case slog.LevelDebug:
		return "debug"
	case slog.LevelWarn:
		return "warn"
	case slog.LevelError:
		return "error"
	default:
		return "info"
	}
}
