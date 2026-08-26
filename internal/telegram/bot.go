package telegram

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// Views are plain data the application adapter supplies; the bot only
// formats them. This keeps every decision in the shared service layer —
// the same backends the web console uses.
type (
	StatusView struct {
		Mode          string
		Ready         bool
		Triangles     int
		Markets       int
		Evaluations   int64
		Qualified     int64
		Rejected      int64
		ConfigVersion int64
		Paper         *PaperView
	}
	PaperView struct {
		Running                              bool
		Active                               int
		Received, Completed, Failed, Skipped int64
	}
	BalanceView struct {
		Asset               string
		Available, Reserved string
	}
	PnLView struct {
		Asset                          string
		Realized, Fees, Loss, Drawdown string
	}
	BookView struct {
		Market, State string
		AgeMS         int64
	}
	FeedView struct {
		Exchange                                     string
		Frames, Reconnects, APIErrors, Resyncs, Gaps int64
		Books                                        []BookView
	}
	BreakerView struct {
		Name, Scope, State string
	}
	OppView struct {
		ID, Triangle, NetBps, Profit, Input string
		At                                  time.Time
	}
)

// Services is the application surface behind every command.
type Services interface {
	Status() StatusView
	Feed() FeedView
	Balances() []BalanceView
	PnL() []PnLView
	Opportunities(limit int) []OppView
	Config() (strategy.Snapshot, bool)
	Breakers() []BreakerView
	Alerts(limit int) []notification.Alert
	AckAlert(id, actor string) (notification.Alert, error)
	PaperPause(actor string) bool  // false = paper engine absent
	PaperResume(actor string) bool // false = paper engine absent
}

// Bot long-polls and dispatches. Allowlisted Telegram IDs act with the
// OPERATOR role; everyone else receives a generic denial only.
type Bot struct {
	Client    *Client
	Allowlist map[int64]bool
	Services  Services
	Log       *slog.Logger
	// Audit records control actions (source=telegram); nil = log only.
	Audit func(actor, action, entity string)
	// PollTimeout for getUpdates (tests use 0 for immediate returns).
	PollTimeout time.Duration

	callbackKey []byte
	mu          sync.Mutex
	pending     map[string]pendingAction // nonce → action awaiting a tap

	msgs atomic.Int64
	errs atomic.Int64
}

type pendingAction struct {
	action  string
	userID  int64
	expires time.Time
}

const callbackTTL = 15 * time.Minute

func (b *Bot) Name() string { return "telegram" }

// Messages / Errors are the metric counters (telegram_messages_total,
// telegram_errors_total).
func (b *Bot) Messages() int64 { return b.msgs.Load() }
func (b *Bot) Errors() int64   { return b.errs.Load() }

func (b *Bot) init() {
	if b.callbackKey == nil {
		b.callbackKey = make([]byte, 32)
		if _, err := rand.Read(b.callbackKey); err != nil {
			panic("telegram: callback key entropy unavailable: " + err.Error())
		}
	}
	if b.pending == nil {
		b.pending = map[string]pendingAction{}
	}
	if b.PollTimeout == 0 {
		b.PollTimeout = 50 * time.Second
	}
}

// Run long-polls until ctx cancels.
func (b *Bot) Run(ctx context.Context) error {
	b.init()
	var offset int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		updates, err := b.Client.GetUpdates(ctx, offset, b.PollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.errs.Add(1)
			b.Log.Warn("telegram poll failed; backing off", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			b.handleUpdate(ctx, u)
		}
	}
}

// HandleUpdate processes one update (exported for the fake-API tests).
func (b *Bot) HandleUpdate(ctx context.Context, u Update) {
	b.init()
	b.handleUpdate(ctx, u)
}

func (b *Bot) handleUpdate(ctx context.Context, u Update) {
	switch {
	case u.Message != nil && u.Message.From != nil:
		b.handleMessage(ctx, u.Message)
	case u.Callback != nil && u.Callback.From != nil:
		b.handleCallback(ctx, u.Callback)
	}
}

func (b *Bot) roleFor(userID int64) (auth.Role, bool) {
	if b.Allowlist[userID] {
		return auth.RoleOperator, true
	}
	return "", false
}

func (b *Bot) handleMessage(ctx context.Context, msg *Message) {
	b.msgs.Add(1)
	role, ok := b.roleFor(msg.From.ID)
	if !ok {
		// Generic denial only: no platform details leak to unknown IDs.
		b.send(ctx, msg.Chat.ID, "Unauthorized.", nil)
		b.Log.Warn("telegram message from unlisted user", "user_id", msg.From.ID)
		return
	}
	cmd := strings.TrimSpace(msg.Text)
	if i := strings.IndexByte(cmd, '@'); i > 0 && strings.HasPrefix(cmd, "/") {
		cmd = cmd[:i] // strip /cmd@BotName addressing
	}
	if i := strings.IndexByte(cmd, ' '); i > 0 {
		cmd = cmd[:i] // arguments are ignored; strict command set only
	}
	actor := fmt.Sprintf("telegram:%d", msg.From.ID)
	text, kb, perm := b.dispatch(cmd, actor)
	if perm != "" && !auth.Can(role, auth.Permission(perm)) {
		b.send(ctx, msg.Chat.ID, "Forbidden: your role lacks "+perm+".", nil)
		return
	}
	if text == "" {
		text = "Unknown command. /help lists the available commands."
	}
	b.send(ctx, msg.Chat.ID, text, kb)
}

func (b *Bot) send(ctx context.Context, chatID int64, text string, kb *InlineKeyboard) {
	if err := b.Client.SendMessage(ctx, chatID, text, kb); err != nil {
		b.errs.Add(1)
		b.Log.Warn("telegram send failed", "error", err)
	}
}

// ---- signed inline callbacks --------------------------------------------

// newCallback stores a pending action and returns opaque signed data:
// v1|nonce|hmac(nonce|action|user). The action itself never travels to
// the client, so payloads cannot be forged or replayed (single use,
// bound to the issuing user, 15-minute expiry).
func (b *Bot) newCallback(action string, userID int64) string {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		panic("telegram: nonce entropy unavailable: " + err.Error())
	}
	n := hex.EncodeToString(nonce)
	b.mu.Lock()
	b.pending[n] = pendingAction{action: action, userID: userID, expires: time.Now().Add(callbackTTL)}
	b.mu.Unlock()
	return "v1|" + n + "|" + b.signCallback(n, action, userID)
}

func (b *Bot) signCallback(nonce, action string, userID int64) string {
	mac := hmac.New(sha256.New, b.callbackKey)
	_, _ = fmt.Fprintf(mac, "%s|%s|%d", nonce, action, userID)
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// takeCallback validates and consumes a callback payload.
func (b *Bot) takeCallback(data string, userID int64) (string, bool) {
	parts := strings.Split(data, "|")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", false
	}
	nonce, sig := parts[1], parts[2]
	b.mu.Lock()
	p, ok := b.pending[nonce]
	if ok {
		delete(b.pending, nonce) // single use, even on failed verification
	}
	b.mu.Unlock()
	if !ok || time.Now().After(p.expires) || p.userID != userID {
		return "", false
	}
	if !hmac.Equal([]byte(sig), []byte(b.signCallback(nonce, p.action, p.userID))) {
		return "", false
	}
	return p.action, true
}

func (b *Bot) handleCallback(ctx context.Context, cb *CallbackQuery) {
	b.msgs.Add(1)
	role, ok := b.roleFor(cb.From.ID)
	if !ok {
		_ = b.Client.AnswerCallback(ctx, cb.ID, "Unauthorized.")
		return
	}
	action, ok := b.takeCallback(cb.Data, cb.From.ID)
	if !ok {
		_ = b.Client.AnswerCallback(ctx, cb.ID, "Expired or invalid button.")
		return
	}
	actor := fmt.Sprintf("telegram:%d", cb.From.ID)
	var reply string
	switch {
	case action == "paper_pause" || action == "paper_resume":
		if !auth.Can(role, auth.PermPaperControl) {
			reply = "Forbidden."
			break
		}
		reply = b.runPaperControl(action, actor)
	case strings.HasPrefix(action, "ack_alert:"):
		if !auth.Can(role, auth.PermAlertAck) {
			reply = "Forbidden."
			break
		}
		id := strings.TrimPrefix(action, "ack_alert:")
		// Telegram actors have no users row: the center stores no
		// acked_by; the audit event carries the concrete actor.
		if _, err := b.Services.AckAlert(id, ""); err != nil {
			reply = "Alert cannot be acknowledged (unknown or already handled)."
			break
		}
		if b.Audit != nil {
			b.Audit(actor, "alert.ack", "alert:"+id)
		}
		reply = "Alert acknowledged."
	default:
		reply = "Unknown action."
	}
	_ = b.Client.AnswerCallback(ctx, cb.ID, reply)
	if cb.Message != nil {
		b.send(ctx, cb.Message.Chat.ID, reply, nil)
	}
}

func (b *Bot) runPaperControl(action, actor string) string {
	var ok bool
	if action == "paper_pause" {
		ok = b.Services.PaperPause(actor)
	} else {
		ok = b.Services.PaperResume(actor)
	}
	if !ok {
		return "Paper engine is not running in this mode."
	}
	if b.Audit != nil {
		b.Audit(actor, action, "paper_engine")
	}
	verb := "resumed"
	if action == "paper_pause" {
		verb = "paused"
	}
	return "Paper engine " + verb + "."
}
