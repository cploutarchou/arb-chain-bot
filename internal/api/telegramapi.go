package api

import (
	"net/http"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// TelegramStatusView is GET /api/v1/telegram/status's wire shape
// (BL-21). It never carries the bot token — only whether the bot is
// configured, who it may talk to (chat ids only), and observed
// connectivity/delivery counters.
type TelegramStatusView struct {
	Enabled   bool    `json:"enabled"`
	Allowlist []int64 `json:"allowlist"`
	// Disabled mirrors telegram.disabled (T-059 §4.2: hot mute); Reason
	// explains a non-running or muted bot ("disabled in settings" /
	// "no token configured" / "allowlist empty at boot").
	Disabled bool   `json:"disabled"`
	Reason   string `json:"reason,omitempty"`

	BotUsername    string     `json:"bot_username,omitempty"`
	Messages       int64      `json:"messages"`
	Errors         int64      `json:"errors"`
	LastPollAt     *time.Time `json:"last_poll_at,omitempty"`
	LastPollOK     bool       `json:"last_poll_ok"`
	LastPollError  string     `json:"last_poll_error,omitempty"`
	LastGetMeAt    *time.Time `json:"last_getme_at,omitempty"`
	LastGetMeOK    bool       `json:"last_getme_ok"`
	LastGetMeError string     `json:"last_getme_error,omitempty"`

	PushesSent   int64      `json:"pushes_sent"`
	PushErrors   int64      `json:"push_errors"`
	LastPushedAt *time.Time `json:"last_pushed_at,omitempty"`
}

// telegramRoutes serves the Telegram status route (BL-21). Reads need
// PermViewSystem, same as system/health and recordings.
func (s *Server) telegramRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/telegram/status", s.requirePerm(auth.PermViewSystem, func(w http.ResponseWriter, r *http.Request) {
		if s.Telegram == nil {
			WriteData(w, http.StatusOK, TelegramStatusView{Enabled: false})
			return
		}
		WriteData(w, http.StatusOK, s.Telegram())
	}))
}
