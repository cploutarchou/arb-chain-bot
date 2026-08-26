package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// fakeAPI implements just enough of the Bot API for the tests: it
// captures sendMessage / answerCallbackQuery calls.
type fakeAPI struct {
	mu       sync.Mutex
	sent     []sentMessage
	answered []string
}

type sentMessage struct {
	ChatID string
	Text   string
	Markup string
}

func (f *fakeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			f.sent = append(f.sent, sentMessage{
				ChatID: r.Form.Get("chat_id"),
				Text:   r.Form.Get("text"),
				Markup: r.Form.Get("reply_markup"),
			})
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			f.answered = append(f.answered, r.Form.Get("text"))
		}
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	})
}

func (f *fakeAPI) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.sent...)
}

func (f *fakeAPI) answers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.answered...)
}

// fakeServices is the shared-state double: the paper flag and the alert
// center are the single backend state the web console would also use.
type fakeServices struct {
	mu           sync.Mutex
	paperRunning bool
	paperPresent bool
	center       *notification.Center
}

func (s *fakeServices) Status() StatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StatusView{Mode: "PAPER", Ready: true, Triangles: 2, Markets: 3,
		Evaluations: 100, Qualified: 5, Rejected: 95, ConfigVersion: 4}
	if s.paperPresent {
		st.Paper = &PaperView{Running: s.paperRunning, Active: 1, Received: 5, Completed: 3, Failed: 1, Skipped: 1}
	}
	return st
}

func (s *fakeServices) Feed() FeedView {
	return FeedView{Exchange: "binance", Frames: 10, Books: []BookView{{Market: "BTCUSDT", State: "HEALTHY", AgeMS: 42}}}
}

func (s *fakeServices) Balances() []BalanceView {
	return []BalanceView{{Asset: "USDT", Available: "9000", Reserved: "1000"}}
}

func (s *fakeServices) PnL() []PnLView {
	return []PnLView{{Asset: "USDT", Realized: "12.5", Fees: "1.2", Loss: "0", Drawdown: "0.0000"}}
}

func (s *fakeServices) Opportunities(int) []OppView {
	return []OppView{{ID: "op-1", Triangle: "binance|USDT|A>B>C", NetBps: "17.20", Profit: "1.72", Input: "1000", At: time.Unix(1_700_000_000, 0)}}
}

func (s *fakeServices) Config() (strategy.Snapshot, bool) {
	return strategy.Snapshot{Version: 4, Params: strategy.DefaultParams(), CreatedAt: time.Unix(1_700_000_000, 0)}, true
}

func (s *fakeServices) Breakers() []BreakerView {
	return []BreakerView{{Name: "exchange", Scope: "exchange:binance", State: "CLOSED"}}
}

func (s *fakeServices) Alerts(limit int) []notification.Alert {
	if s.center == nil {
		return nil
	}
	return s.center.List("", limit)
}

func (s *fakeServices) AckAlert(id, actor string) (notification.Alert, error) {
	if s.center == nil {
		return notification.Alert{}, notification.ErrAlertNotFound
	}
	return s.center.Ack(id, actor)
}

func (s *fakeServices) PaperPause(string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paperPresent {
		return false
	}
	s.paperRunning = false
	return true
}

func (s *fakeServices) PaperResume(string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paperPresent {
		return false
	}
	s.paperRunning = true
	return true
}

func (s *fakeServices) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paperRunning
}

func newTestBot(t *testing.T) (*Bot, *fakeAPI, *fakeServices, func(actor, action, entity string) []string) {
	t.Helper()
	fake := &fakeAPI{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	var auditMu sync.Mutex
	var audits []string
	center := &notification.Center{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDGen: newSeqIDGen(),
	}
	center.Deliver(notification.Delivery{Event: notification.Event{
		Severity: notification.SeverityWarning, Key: "gap:BTCUSDT",
		Title: "Gap storm", Body: "many gaps", At: time.Unix(1_700_000_000, 0),
	}, Suppressed: 3})
	svcs := &fakeServices{paperRunning: true, paperPresent: true, center: center}
	bot := &Bot{
		Client:    NewClient(srv.URL + "/botTEST"),
		Allowlist: map[int64]bool{100: true},
		Services:  svcs,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: func(actor, action, entity string) {
			auditMu.Lock()
			audits = append(audits, actor+"|"+action+"|"+entity)
			auditMu.Unlock()
		},
	}
	getAudits := func(string, string, string) []string {
		auditMu.Lock()
		defer auditMu.Unlock()
		return append([]string(nil), audits...)
	}
	return bot, fake, svcs, getAudits
}

func msgUpdate(userID, chatID int64, text string) Update {
	return Update{UpdateID: 1, Message: &Message{
		From: &User{ID: userID}, Chat: Chat{ID: chatID}, Text: text,
	}}
}

func TestUnlistedUserGetsGenericDenialOnly(t *testing.T) {
	bot, fake, _, _ := newTestBot(t)
	bot.HandleUpdate(context.Background(), msgUpdate(999, 999, "/status"))
	got := fake.messages()
	if len(got) != 1 || got[0].Text != "Unauthorized." {
		t.Fatalf("denial = %+v", got)
	}
}

func TestCommandsAnswerFromSharedServices(t *testing.T) {
	bot, fake, _, _ := newTestBot(t)
	ctx := context.Background()
	for _, cmd := range []string{"/status", "/scanner", "/balances", "/pnl", "/exchanges", "/risk", "/alerts", "/config", "/opportunities", "/help"} {
		bot.HandleUpdate(ctx, msgUpdate(100, 100, cmd))
	}
	got := fake.messages()
	if len(got) != 10 {
		t.Fatalf("replies = %d", len(got))
	}
	checks := map[int]string{
		0: "Mode: PAPER",
		1: "Qualified: 5",
		2: "USDT  available 9000",
		3: "realized 12.5",
		4: "binance feed",
		5: "min net edge: 5 bps",
		6: "Gap storm",
		7: "version 4",
		8: "17.20 bps",
		9: "/paper_pause",
	}
	for i, want := range checks {
		if !strings.Contains(got[i].Text, want) {
			t.Errorf("reply %d missing %q:\n%s", i, want, got[i].Text)
		}
	}
	// /alerts must surface the aggregation count (1 delivery + 3
	// suppressed = ×4) and offer an Ack button for the active alert.
	if !strings.Contains(got[6].Text, "(×4)") {
		t.Errorf("alerts reply lacks aggregation count: %s", got[6].Text)
	}
	if got[6].Markup == "" || !strings.Contains(got[6].Markup, "Ack 1") {
		t.Errorf("alerts reply lacks ack button: %s", got[6].Markup)
	}
}

// newSeqIDGen returns deterministic alert IDs for tests.
func newSeqIDGen() func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return "alert-" + string(rune('0'+n))
	}
}

func TestAlertAckButtonSharesLifecycleWithWeb(t *testing.T) {
	bot, fake, svcs, audits := newTestBot(t)
	ctx := context.Background()

	bot.HandleUpdate(ctx, msgUpdate(100, 100, "/alerts"))
	msgs := fake.messages()
	var kb struct {
		InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(msgs[0].Markup), &kb); err != nil {
		t.Fatal(err)
	}
	bot.HandleUpdate(ctx, Update{UpdateID: 2, Callback: &CallbackQuery{
		ID: "cb1", From: &User{ID: 100},
		Message: &Message{Chat: Chat{ID: 100}}, Data: kb.InlineKeyboard[0][0].Data,
	}})
	if ans := fake.answers(); len(ans) != 1 || !strings.Contains(ans[0], "acknowledged") {
		t.Fatalf("answers = %v", ans)
	}
	// The same center the web console reads now shows acked.
	alerts := svcs.center.List("", 0)
	if len(alerts) != 1 || alerts[0].State != notification.AlertAcked {
		t.Fatalf("center state = %+v", alerts)
	}
	if got := audits("", "", ""); len(got) != 1 || !strings.Contains(got[0], "alert.ack") {
		t.Fatalf("audits = %v", got)
	}
	// Second ack of the same alert is rejected (already acked).
	bot.HandleUpdate(ctx, msgUpdate(100, 100, "/alerts"))
	msgs = fake.messages()
	last := msgs[len(msgs)-1]
	if strings.Contains(last.Markup, "Ack 1") {
		t.Fatalf("acked alert still offers ack button: %s", last.Markup)
	}
}

func TestPaperControlSharesStateAndAudits(t *testing.T) {
	bot, fake, svcs, audits := newTestBot(t)
	ctx := context.Background()

	bot.HandleUpdate(ctx, msgUpdate(100, 100, "/paper_pause"))
	if svcs.running() {
		t.Fatal("pause did not flip the shared backend state")
	}
	bot.HandleUpdate(ctx, msgUpdate(100, 100, "/paper_resume"))
	if !svcs.running() {
		t.Fatal("resume did not flip the shared backend state")
	}
	got := audits("", "", "")
	if len(got) != 2 || got[0] != "telegram:100|paper_pause|paper_engine" {
		t.Fatalf("audits = %v", got)
	}
	msgs := fake.messages()
	if !strings.Contains(msgs[0].Text, "paused") || !strings.Contains(msgs[1].Text, "resumed") {
		t.Fatalf("control replies = %+v", msgs)
	}
}

func TestInlineButtonRoundTripAndTamperRejection(t *testing.T) {
	bot, fake, svcs, _ := newTestBot(t)
	ctx := context.Background()

	// /paper renders buttons with signed callback data.
	bot.HandleUpdate(ctx, msgUpdate(100, 100, "/paper"))
	msgs := fake.messages()
	if len(msgs) != 1 || msgs[0].Markup == "" {
		t.Fatalf("no keyboard: %+v", msgs)
	}
	var kb struct {
		InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(msgs[0].Markup), &kb); err != nil {
		t.Fatal(err)
	}
	pauseData := kb.InlineKeyboard[0][0].Data

	// A tap with valid data executes and answers.
	bot.HandleUpdate(ctx, Update{UpdateID: 2, Callback: &CallbackQuery{
		ID: "cb1", From: &User{ID: 100},
		Message: &Message{Chat: Chat{ID: 100}}, Data: pauseData,
	}})
	if svcs.running() {
		t.Fatal("button tap did not pause")
	}
	if ans := fake.answers(); len(ans) != 1 || !strings.Contains(ans[0], "paused") {
		t.Fatalf("answers = %v", ans)
	}

	// Replay of the same nonce is rejected (single use).
	bot.HandleUpdate(ctx, Update{UpdateID: 3, Callback: &CallbackQuery{
		ID: "cb2", From: &User{ID: 100}, Data: pauseData,
	}})
	if ans := fake.answers(); len(ans) != 2 || !strings.Contains(ans[1], "Expired or invalid") {
		t.Fatalf("replay answers = %v", ans)
	}

	// Tampered payload is rejected.
	resumeData := kb.InlineKeyboard[0][1].Data
	tampered := resumeData[:len(resumeData)-1] + "0"
	bot.HandleUpdate(ctx, Update{UpdateID: 4, Callback: &CallbackQuery{
		ID: "cb3", From: &User{ID: 100}, Data: tampered,
	}})
	if svcs.running() {
		t.Fatal("tampered callback must not execute")
	}

	// A different user cannot use another user's button.
	bot.HandleUpdate(ctx, msgUpdate(100, 100, "/paper"))
	msgs = fake.messages()
	var kb2 struct {
		InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
	}
	_ = json.Unmarshal([]byte(msgs[len(msgs)-1].Markup), &kb2)
	bot.Allowlist[200] = true
	bot.HandleUpdate(ctx, Update{UpdateID: 5, Callback: &CallbackQuery{
		ID: "cb4", From: &User{ID: 200}, Data: kb2.InlineKeyboard[0][1].Data,
	}})
	if svcs.running() {
		t.Fatal("cross-user callback must not execute")
	}
}

func TestPaperAbsentIsHonest(t *testing.T) {
	bot, fake, svcs, _ := newTestBot(t)
	svcs.mu.Lock()
	svcs.paperPresent = false
	svcs.mu.Unlock()
	bot.HandleUpdate(context.Background(), msgUpdate(100, 100, "/paper_pause"))
	got := fake.messages()
	if len(got) != 1 || !strings.Contains(got[0].Text, "not running") {
		t.Fatalf("absent reply = %+v", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	bot, fake, _, _ := newTestBot(t)
	bot.HandleUpdate(context.Background(), msgUpdate(100, 100, "/frobnicate"))
	got := fake.messages()
	if len(got) != 1 || !strings.Contains(got[0].Text, "Unknown command") {
		t.Fatalf("unknown = %+v", got)
	}
}

func TestRunLoopPollsFakeAPI(t *testing.T) {
	fake := &fakeAPI{}
	mux := http.NewServeMux()
	var polls atomic32
	mux.HandleFunc("/botTEST/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		if polls.inc() == 1 {
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":7,"message":{"message_id":1,"from":{"id":100},"chat":{"id":100},"text":"/status"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	})
	mux.Handle("/botTEST/sendMessage", fake.handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bot := &Bot{
		Client:      NewClient(srv.URL + "/botTEST"),
		Allowlist:   map[int64]bool{100: true},
		Services:    &fakeServices{paperPresent: true},
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollTimeout: time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		for {
			if len(fake.messages()) > 0 {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	_ = bot.Run(ctx)
	got := fake.messages()
	if len(got) == 0 || !strings.Contains(got[0].Text, "Mode:") {
		t.Fatalf("poll loop replies = %+v", got)
	}
	if bot.Messages() == 0 {
		t.Fatal("message counter not bumped")
	}
}

type atomic32 struct {
	mu sync.Mutex
	n  int
}

func (a *atomic32) inc() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	return a.n
}
