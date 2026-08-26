package app

import (
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

// NewLogger builds the process logger: structured JSON on stdout with the
// configured level and unconditional redaction of sensitive attributes.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, sensitive := sensitiveKeys[strings.ToLower(a.Key)]; sensitive {
				a.Value = slog.StringValue("***")
			}
			return a
		},
	})
	return slog.New(h)
}
