package marketdata

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// ErrRecorderRunning and ErrRecorderIdle are the control-surface
// conflicts (HTTP 409 at the API).
var (
	ErrRecorderRunning = errors.New("recorder: a session is already running")
	ErrRecorderIdle    = errors.New("recorder: no session is running")
)

// RecorderStatus is the live view of the active session (nil-safe zero
// value = idle).
type RecorderStatus struct {
	Running   bool       `json:"running"`
	SessionID string     `json:"session_id,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	Written   int64      `json:"frames_written"`
	Dropped   int64      `json:"frames_dropped"`
	// QueueDepth/QueueCapacity are the tap-queue backlog (BL-18); zero
	// while idle (no Recorder to probe).
	QueueDepth    int               `json:"queue_depth"`
	QueueCapacity int               `json:"queue_capacity"`
	Segments      int               `json:"segments_closed"`
	Bytes         int64             `json:"bytes_closed"`
	Symbols       []exchange.Symbol `json:"symbols,omitempty"`
	Dir           string            `json:"dir,omitempty"`
}

// RecorderControl owns at most one live Recorder and lets the API start
// and stop sessions in-process. The feed's taps point at the control, so
// frames flow to whichever session is active and are dropped on the
// floor while idle. A Recorder is single-use (its queue is created once),
// so every Start builds a fresh one with a fresh session id.
type RecorderControl struct {
	Dir            string
	Log            *slog.Logger
	StreamOfSymbol map[exchange.Symbol]uint16
	NewSessionID   func() string
	// OnSegment is invoked with the session id for every closed segment;
	// the engine registers metadata rows through it.
	OnSegment func(sessionID string, startedAt time.Time, meta SegmentMeta)
	// OnChange, when set, fires after every start/stop with the new
	// status (realtime fan-out).
	OnChange func(RecorderStatus)

	mu       sync.Mutex
	lifetime context.Context
	rec      *Recorder
	cancel   context.CancelFunc
	done     chan struct{}
	started  time.Time
	segs     int
	bytes    int64
	runErr   error
}

// TapWS forwards a raw WS frame to the active session, if any.
func (c *RecorderControl) TapWS(frame []byte, recv time.Time) {
	if r := c.active(); r != nil {
		r.TapWS(frame, recv)
	}
}

// TapSnapshot forwards a REST snapshot to the active session, if any.
func (c *RecorderControl) TapSnapshot(symbol exchange.Symbol, body []byte, recv time.Time) {
	if r := c.active(); r != nil {
		r.TapSnapshot(symbol, body, recv)
	}
}

func (c *RecorderControl) active() *Recorder {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rec
}

// Bind pins the lifetime context sessions started via StartSession
// inherit (the engine's run context), so API-started sessions end with
// the engine rather than with the HTTP request.
func (c *RecorderControl) Bind(ctx context.Context) {
	c.mu.Lock()
	c.lifetime = ctx
	c.mu.Unlock()
}

// StartSession is Start bound to the lifetime context (background when
// none was bound).
func (c *RecorderControl) StartSession() (string, error) {
	c.mu.Lock()
	ctx := c.lifetime
	c.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return c.Start(ctx)
}

// Start opens a new session and returns its id. The session runs until
// Stop or until ctx is cancelled (engine shutdown), in which case the
// final segment is drained and registered exactly as on Stop.
func (c *RecorderControl) Start(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.rec != nil {
		c.mu.Unlock()
		return "", ErrRecorderRunning
	}
	id := c.NewSessionID()
	startedAt := time.Now().UTC()
	log := c.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	rec := &Recorder{
		Dir:            c.Dir,
		SessionID:      id,
		Log:            log,
		StreamOfSymbol: c.StreamOfSymbol,
	}
	rec.OnSegment = func(meta SegmentMeta) {
		c.mu.Lock()
		if c.rec == rec {
			c.segs++
			c.bytes += meta.Bytes
		}
		c.mu.Unlock()
		if c.OnSegment != nil {
			c.OnSegment(id, startedAt, meta)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c.rec, c.cancel, c.done, c.started, c.segs, c.bytes, c.runErr = rec, cancel, done, startedAt, 0, 0, nil
	c.mu.Unlock()

	go func() {
		err := rec.Run(runCtx)
		c.mu.Lock()
		if c.rec == rec {
			if err != nil && !errors.Is(err, context.Canceled) {
				c.runErr = err
			}
			c.rec, c.cancel, c.done = nil, nil, nil
		}
		status := c.statusLocked()
		c.mu.Unlock()
		close(done)
		if c.Log != nil {
			c.Log.Info("recording session ended", "session", id, "error", err)
		}
		if c.OnChange != nil {
			c.OnChange(status)
		}
	}()
	if c.Log != nil {
		c.Log.Info("recording enabled", "dir", c.Dir, "session", id)
	}
	if c.OnChange != nil {
		c.OnChange(c.Status())
	}
	return id, nil
}

// Stop ends the active session and waits until its last segment is
// closed and registered, so callers can immediately run a campaign on
// it. Returns the session id that was stopped.
func (c *RecorderControl) Stop() (string, error) {
	c.mu.Lock()
	if c.rec == nil {
		c.mu.Unlock()
		return "", ErrRecorderIdle
	}
	id, cancel, done := c.rec.SessionID, c.cancel, c.done
	c.mu.Unlock()
	cancel()
	<-done
	return id, nil
}

// Status is the live snapshot of the control.
func (c *RecorderControl) Status() RecorderStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *RecorderControl) statusLocked() RecorderStatus {
	st := RecorderStatus{Dir: c.Dir}
	if c.rec == nil {
		return st
	}
	started := c.started
	st.Running = true
	st.SessionID = c.rec.SessionID
	st.StartedAt = &started
	st.Written = c.rec.Written()
	st.Dropped = c.rec.Dropped()
	st.QueueDepth = c.rec.Depth()
	st.QueueCapacity = c.rec.Capacity()
	st.Segments = c.segs
	st.Bytes = c.bytes
	for sym := range c.StreamOfSymbol {
		st.Symbols = append(st.Symbols, sym)
	}
	sortSymbols(st.Symbols)
	return st
}

// Err reports the last session's fatal error, if any (e.g. the
// recordings directory became unwritable).
func (c *RecorderControl) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runErr
}

func sortSymbols(s []exchange.Symbol) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
