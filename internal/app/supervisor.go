package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/paper"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// EngineRunner is the surface Supervisor re-enters across a restart
// (T-057 design §2.3). It is deliberately minimal so tests can supply a
// fake instead of a real *Engine.
type EngineRunner interface {
	Run(ctx context.Context) error
	ApplySettings(s platform.Settings, version int64)
}

// RestartState is the supervisor's own state machine.
type RestartState string

const (
	StateReady      RestartState = "ready"
	StatePending    RestartState = "pending"
	StateRestarting RestartState = "restarting"
	StateFailed     RestartState = "failed"
)

// RestartStatus is served on the "health" topic and GET /api/v1/engine/status.
type RestartStatus struct {
	State           RestartState `json:"state"`
	SettingsVersion int64        `json:"settings_version"`          // version the engine is running
	PendingVersion  int64        `json:"pending_version,omitempty"` // newer version with restart-scoped changes
	RequestedBy     string       `json:"requested_by,omitempty"`
	RequestedAt     *time.Time   `json:"requested_at,omitempty"`
	ReadyAt         *time.Time   `json:"ready_at,omitempty"`
	Restarts        int64        `json:"restarts"`
	PendingReasons  []string     `json:"pending_reasons,omitempty"`
	Error           string       `json:"error,omitempty"`
}

// RestartRequest is one restart ask.
type RestartRequest struct {
	Actor, Reason string
	StopRecording bool
	// Done, when non-nil, receives at most one error (possibly nil) and
	// is then closed, once the new run is ready or has failed.
	// Buffered(1) so the sender never blocks the supervisor loop.
	Done chan error
}

// CampaignBusy reports whether a campaign run is in progress (and its
// id) — campaign.Runner.BusyRun satisfies this. Named distinctly from
// campaign.Runner.Busy() (bool), which existing callers/tests already
// use and which this feature leaves untouched (a Go type cannot have
// two methods named Busy with different signatures).
type CampaignBusy interface {
	BusyRun() (string, bool)
}

// Guard-rail refusals (design §2.5); the API maps these to HTTP codes.
var (
	ErrCampaignRunning   = errors.New("engine: campaign run in progress")
	ErrRecordingActive   = errors.New("engine: recording session active")
	ErrRestartInProgress = errors.New("engine: a restart is already in progress")
)

// Supervisor runs Engine.Run in a loop, applying restart-scoped platform
// settings changes between runs (design §2.2-§2.6). It is itself an
// app.Component; BuildComponents appends it where it used to append the
// engine directly (D4: the *Engine value never changes).
type Supervisor struct {
	Engine   EngineRunner
	Settings *platform.Service
	Recorder func() *marketdata.RecorderControl
	Paper    func() *paper.Engine
	// Campaigns, when set, refuses a restart while a run is in progress.
	Campaigns CampaignBusy
	Grace     time.Duration
	Log       *slog.Logger
	// Notify mirrors Engine.notify's signature (severity, key, title, body).
	Notify func(sev notification.Severity, key, title, body string)
	// OnState, when set, is called after every state change (hub publish
	// on "health"). Must be nil-safe to call from any profile, including
	// ones with no API/Hub attached (e.g. ProfileScanner).
	OnState func(RestartStatus)
	// Audit records restart/failure actions (source-agnostic, mirrors
	// the sourceAudit/webAudit simple 3-arg convention already used for
	// paper/recorder/campaign control actions — richer before/after
	// payloads belong to a domain service's own audit hook, as
	// platform.Service already provides for settings.apply/rollback).
	Audit func(actor, action, entity string)

	// Strategy and RunningWorkers, when both set, extend PendingReasons
	// with "strategy vN (scanner.workers)" — the strategy document's
	// only restart-scoped field (design §2.3 "one restart banner, two
	// documents"). Neither is part of the design's literal Supervisor
	// field list; both are additive and optional, so a caller that
	// omits them only loses that one extra pending-reason source.
	Strategy       *strategy.Service
	RunningWorkers func() int
	// Ready, when set, is polled after (re-)launching the engine so the
	// state can flip from "restarting" to "ready" on the engine's own
	// bootstrap completion (Engine.Status().Ready) rather than merely
	// "the goroutine has not errored yet". Optional: the design's
	// minimal EngineRunner has no Status(), so tests may omit this.
	Ready func() bool
	// EndPaperSession closes the outgoing paper session row (E10) after
	// a restart drains it. Optional: nil when persistence is off.
	EndPaperSession func(ctx context.Context, sessionID string, at time.Time) error
	// SessionID returns the engine's current paper session id (input to
	// EndPaperSession). Optional; only consulted when EndPaperSession is set.
	SessionID func() string

	initOnce sync.Once
	reqs     chan RestartRequest
	errCh    chan error

	subscribedOnce sync.Once

	mu              sync.Mutex
	st              RestartStatus
	runningSettings platform.Settings
	pendingPlatform string
	pendingWorkers  string
}

func (s *Supervisor) Name() string { return "engine-supervisor" }

func (s *Supervisor) init() {
	s.initOnce.Do(func() {
		s.reqs = make(chan RestartRequest, 1)
		s.errCh = make(chan error, 1)
		if s.Log == nil {
			s.Log = slog.New(slog.DiscardHandler)
		}
		if s.Grace <= 0 {
			s.Grace = 15 * time.Second
		}
	})
}

// Status returns the current restart status.
func (s *Supervisor) Status() RestartStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// setStatus mutates the status under lock and publishes the snapshot.
// fn must not itself lock s.mu (not reentrant).
func (s *Supervisor) setStatus(fn func(*RestartStatus)) {
	s.mu.Lock()
	fn(&s.st)
	snapshot := s.st
	s.mu.Unlock()
	if s.OnState != nil {
		s.OnState(snapshot)
	}
}

// Request validates the guard rails (design §2.5, in order) and enqueues
// the restart; the sequence itself runs on Supervisor.Run's goroutine.
func (s *Supervisor) Request(req RestartRequest) error {
	s.init()
	if s.Campaigns != nil {
		if id, busy := s.Campaigns.BusyRun(); busy {
			return fmt.Errorf("%w: campaign run %s is in progress", ErrCampaignRunning, id)
		}
	}
	if !req.StopRecording && s.Recorder != nil {
		if rc := s.Recorder(); rc != nil {
			if st := rc.Status(); st.Running {
				return fmt.Errorf("%w: recording session %s is active — stop it, or resend with stop_recording", ErrRecordingActive, st.SessionID)
			}
		}
	}
	s.mu.Lock()
	if s.st.State == StateRestarting {
		s.mu.Unlock()
		return ErrRestartInProgress
	}
	now := time.Now()
	s.st.State = StateRestarting
	s.st.RequestedBy = req.Actor
	s.st.RequestedAt = &now
	s.st.Error = ""
	snapshot := s.st
	s.mu.Unlock()
	if s.OnState != nil {
		s.OnState(snapshot)
	}
	if s.Notify != nil {
		s.Notify(notification.SeverityInfo, "engine:restart", "Engine restart requested",
			fmt.Sprintf("requested by %s: %s", req.Actor, req.Reason))
	}
	select {
	case s.reqs <- req:
		return nil
	default:
		// The state gate above should make this unreachable (cap-1
		// channel, state already flipped to restarting), but stay
		// defensive rather than block the caller forever.
		return ErrRestartInProgress
	}
}

// Run starts the engine and services restart requests until ctx cancels.
func (s *Supervisor) Run(ctx context.Context) error {
	s.init()

	snap := s.Settings.Current()
	cancelEngine := s.launch(ctx, snap)
	if err := s.waitReadyOrFail(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Restarts is necessarily 0 here (markReady has not run yet):
		// today's fail-fast boot semantics, preserved exactly.
		return err
	}
	// subscribeSettings is registered only AFTER the first launch: both
	// Settings.Subscribe and strategy.Service.Subscribe deliver the
	// CURRENT snapshot immediately on registration, comparing it
	// against s.runningSettings (set by launch->startRun) and
	// RunningWorkers() (the just-started engine's own config, copied
	// from this same strategy snapshot at Run entry) — registering
	// before the first launch would compare against a zero-value
	// baseline and spuriously raise the restart banner at boot.
	s.subscribeSettings()
	s.markReady(snap.Version)
	engineAlive := true

	for {
		select {
		case <-ctx.Done():
			if engineAlive {
				cancelEngine()
				s.waitEngineStop(s.Grace)
			}
			return ctx.Err()

		case err := <-s.errCh:
			engineAlive = false
			if err != nil && !errors.Is(err, context.Canceled) {
				if s.Status().Restarts == 0 {
					// Today's fail-fast boot semantics, preserved exactly.
					return err
				}
				s.markFailed(err)
			}

		case req := <-s.reqs:
			cancelEngine = s.restart(ctx, req, cancelEngine, engineAlive)
			engineAlive = s.Status().State != StateFailed
		}
	}
}

// launch starts the engine against snap and returns its cancel func.
func (s *Supervisor) launch(parent context.Context, snap platform.Snapshot) context.CancelFunc {
	s.startRun(snap.Settings, snap.Version)
	ctx, cancel := context.WithCancel(parent)
	go func() { s.errCh <- s.Engine.Run(ctx) }()
	return cancel
}

// restart executes design §2.4 steps 2-5. cancelCurrent/engineAlive
// describe the run in flight when the request arrived; when engineAlive
// is false (e.g. a prior failure left the supervisor idle) the
// cancel-and-wait step is skipped entirely.
func (s *Supervisor) restart(parent context.Context, req RestartRequest, cancelCurrent context.CancelFunc, engineAlive bool) context.CancelFunc {
	done := func(err error) {
		if req.Done != nil {
			req.Done <- err
			close(req.Done)
		}
	}

	// Step 2: stop recording BEFORE cancelling — both paths drain the
	// last segment identically, but stop-first gives a deterministic
	// "recording stopped by restart" event and the session id to audit.
	if req.StopRecording && s.Recorder != nil {
		if rc := s.Recorder(); rc != nil {
			_, _ = rc.Stop()
		}
	}

	// Step 3: capture and pause paper BEFORE cancelling — no new cycles
	// enter the queue while legs settle.
	var wasRunning bool
	if s.Paper != nil {
		if pe := s.Paper(); pe != nil {
			wasRunning = pe.Running()
			pe.Pause()
		}
	}

	outgoingSession := ""
	if s.SessionID != nil {
		outgoingSession = s.SessionID()
	}

	// Step 4: cancel and WAIT for the previous run to fully return
	// (bounded by Grace) — strictly sequential; re-entering early races
	// two feeds on e.scn.
	if engineAlive {
		if cancelCurrent != nil {
			cancelCurrent()
		}
		if !s.waitEngineStop(s.Grace) {
			s.markFailed(fmt.Errorf("engine: restart timed out waiting for the previous run to stop (grace %s)", s.Grace))
			done(errors.New("restart timed out"))
			// No re-entry: return a no-op cancel; the caller's next
			// Request starts fresh from an idle (engineAlive=false) state.
			return func() {}
		}
	}
	if s.EndPaperSession != nil && outgoingSession != "" {
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.EndPaperSession(endCtx, outgoingSession, time.Now()); err != nil {
			s.Log.Warn("end paper session failed", "session", outgoingSession, "error", err)
		}
		cancel()
	}

	// Step 5: re-enter.
	snap := s.Settings.Current()
	newCancel := s.launch(parent, snap)
	if err := s.waitReadyOrFail(); err != nil {
		if parent.Err() != nil {
			// Parent shutting down concurrently with the restart: not a
			// restart failure, let the outer Run loop's ctx.Done() case
			// take over on its next iteration.
			done(nil)
			return newCancel
		}
		s.markFailed(fmt.Errorf("engine: restart failed: %w", err))
		done(fmt.Errorf("restart failed: %w", err))
		return func() {}
	}
	s.markReady(snap.Version)

	if !wasRunning && s.Paper != nil {
		if pe := s.Paper(); pe != nil {
			pe.Pause()
		}
	}

	if s.Audit != nil {
		s.Audit(req.Actor, "engine.restart", "engine")
	}
	done(nil)
	return newCancel
}

// waitEngineStop waits up to grace for the run in flight to signal
// completion on s.errCh, discarding the value (the caller has already
// decided how to react to a fatal error via the normal errCh case; this
// path exists only to guarantee sequencing before re-entry). Returns
// false on timeout.
func (s *Supervisor) waitEngineStop(grace time.Duration) bool {
	select {
	case <-s.errCh:
		return true
	case <-time.After(grace):
		return false
	}
}

// waitReadyOrFail polls Ready (when set) so state can flip to ready on
// the engine's own bootstrap completion rather than merely "the
// goroutine has not errored yet" — and watches s.errCh at the same time,
// so an engine that fails immediately (e.g. a bad settings document a
// dry-run should have caught, but didn't) is detected in milliseconds
// instead of only after the full poll deadline. Returns the error the
// engine exited with, if it exited before becoming ready; nil otherwise
// (including when Ready is unset, in which case launch is assumed
// successful once the goroutine is running).
func (s *Supervisor) waitReadyOrFail() error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-s.errCh:
			return err
		default:
		}
		if s.Ready == nil || s.Ready() {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Supervisor) startRun(settings platform.Settings, version int64) {
	s.Engine.ApplySettings(settings, version)
	s.mu.Lock()
	s.runningSettings = settings.Clone()
	s.mu.Unlock()
}

func (s *Supervisor) markReady(version int64) {
	now := time.Now()
	// A restart resolves whatever was pending against the settings it
	// just applied; recomputePending below will re-raise the banner if
	// (and only if) something newer landed while the restart was
	// already in flight.
	s.mu.Lock()
	s.pendingPlatform = ""
	s.pendingWorkers = ""
	s.mu.Unlock()
	s.setStatus(func(st *RestartStatus) {
		st.State = StateReady
		st.SettingsVersion = version
		st.PendingVersion = 0
		st.ReadyAt = &now
		st.Restarts++
		st.Error = ""
	})
	s.recomputePending()
}

func (s *Supervisor) markFailed(err error) {
	s.setStatus(func(st *RestartStatus) {
		st.State = StateFailed
		st.Error = err.Error()
	})
	if s.Notify != nil {
		s.Notify(notification.SeverityCritical, "engine:restart_failed", "Engine restart failed", err.Error())
	}
	if s.Audit != nil {
		s.Audit("system", "engine.restart.failed", "engine")
	}
}

// subscribeSettings registers the Settings.Subscribe (and, when
// available, strategy.Service.Subscribe) callbacks that compute
// PendingReasons (design §2.3). Runs at most once per Supervisor.
func (s *Supervisor) subscribeSettings() {
	s.subscribedOnce.Do(func() {
		if s.Settings != nil {
			s.Settings.Subscribe(func(newSnap platform.Snapshot) {
				s.mu.Lock()
				running := s.runningSettings
				s.mu.Unlock()
				diff, err := strategy.DiffAny(running, newSnap.Settings)
				if err != nil {
					return
				}
				s.mu.Lock()
				if len(diff) > 0 && running.RestartScoped(diff) {
					s.pendingPlatform = fmt.Sprintf("platform settings v%d", newSnap.Version)
					s.st.PendingVersion = newSnap.Version
				} else {
					s.pendingPlatform = ""
				}
				s.mu.Unlock()
				s.recomputePending()
			})
		}
		if s.Strategy != nil && s.RunningWorkers != nil {
			s.Strategy.Subscribe(func(snap strategy.Snapshot) {
				running := s.RunningWorkers()
				s.mu.Lock()
				if snap.Params.Scanner.Workers != running {
					s.pendingWorkers = fmt.Sprintf("strategy v%d (scanner.workers)", snap.Version)
				} else {
					s.pendingWorkers = ""
				}
				s.mu.Unlock()
				s.recomputePending()
			})
		}
	})
}

// recomputePending folds pendingPlatform/pendingWorkers into
// RestartStatus.PendingReasons and flips ready<->pending accordingly,
// never touching restarting/failed.
func (s *Supervisor) recomputePending() {
	s.mu.Lock()
	var reasons []string
	if s.pendingPlatform != "" {
		reasons = append(reasons, s.pendingPlatform)
	}
	if s.pendingWorkers != "" {
		reasons = append(reasons, s.pendingWorkers)
	}
	s.st.PendingReasons = reasons
	switch s.st.State {
	case StateReady:
		if len(reasons) > 0 {
			s.st.State = StatePending
		}
	case StatePending:
		if len(reasons) == 0 {
			s.st.State = StateReady
		}
	}
	snapshot := s.st
	s.mu.Unlock()
	if s.OnState != nil {
		s.OnState(snapshot)
	}
}
