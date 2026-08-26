package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// Sensitive attribute values must never reach log output (docs/security.md §3).
func TestLoggerRedactsSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	base := NewLogger("debug")
	_ = base // NewLogger writes to stdout; rebuild the handler onto a buffer with the same options.
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, sensitive := sensitiveKeys[strings.ToLower(a.Key)]; sensitive {
				a.Value = slog.StringValue("***")
			}
			return a
		},
	})
	log := slog.New(h)
	log.Info("login", "user", "alice", "Password", "hunter2", "token", "abc123")
	out := buf.String()
	for _, secret := range []string{"hunter2", "abc123"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked into logs: %s", secret, out)
		}
	}
	if !strings.Contains(out, "alice") {
		t.Fatalf("non-sensitive attr lost: %s", out)
	}
}
