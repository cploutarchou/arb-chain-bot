package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
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
	// is then closed, once the new run is ready (or confirmed still
	// bootstrapping past ReadyTimeout, P2-2 — that outcome is reported
	// as accepted, nil, since it is not a failure) or has failed.
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

// ReplayBusy reports whether a console-driven replay run (BL-17) is in
// progress — replay.Runner.BusyRun satisfies this. A replay run never
// touches the live *Engine (backtest.Run builds its own books, scanner,
// and portfolio in-process), so a restart cannot corrupt one; the guard
// exists only so a restart doesn't silently fight a replay for the same
// CPU core, giving the operator an honest reason instead of unexplained
// slowness on both sides.
type ReplayBusy interface {
	BusyRun() (string, bool)
}

// Guard-rail refusals (design §2.5); the API maps these to HTTP codes.
var (
	ErrCampaignRunning   = errors.New("engine: campaign run in progress")
	ErrReplayRunning     = errors.New("engine: replay run in progress")
	ErrRecordingActive   = errors.New("engine: recording session active")
	ErrRestartInProgress = errors.New("engine: a restart is already in progress")
)

// runExit is one engine generation's terminal result (P1-1). Tagging
// every value on errCh with the generation that produced it is what
// lets waitEngineStop/waitReadyOrFail/Run's main loop tell "the run I am
// tracking just exited" apart from "a PREVIOUS, already-abandoned run's
// exit finally arrived". Before this fix, a grace-timeout on the
// cancel-and-wait step discarded the abandoned run's cancel func
// entirely and let the NEXT restart launch a second Engine.Run while the
// first one might still be executing; when the abandoned run finally
// did return, its (untagged) exit was read by whichever restart
// happened to be waiting at the time and misattributed to it, corrupting
// engineAlive bookkeeping — in the worst case letting a THIRD restart
// launch while a second one was still live, two feeds/scanners/outboxes
// racing on the same *Engine.
type runExit struct {
	gen uint64
	err error
}

// errReadyTimeout is waitReadyOrFail's sentinel for "the deadline
// elapsed and Ready() still reports false, but the run has not exited
// either" (P2-2) — distinct from a nil error (became ready) and from a
// non-nil, non-sentinel error (the run actually exited before becoming
// ready). The caller must NOT mark the state ready on this outcome: it
// stays "restarting" (bootstrapping) until Run's readiness ticker
// confirms Ready() later, or the run's actual exit arrives.
var errReadyTimeout = errors.New("supervisor: engine did not report ready before the deadline")

// defaultReadyTimeout / readyPollInterval (P3-14): the readiness wait
// used to be a hardcoded 30s deadline polled via time.Sleep(10ms) in a
// tight loop. The deadline is now Supervisor.ReadyTimeout (defaulting to
// the same 30s), and the poll is a ticker.
const (
	defaultReadyTimeout = 30 * time.Second
	readyPollInterval   = 100 * time.Millisecond
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
	// Replays, when set, refuses a restart while a replay run (BL-17) is
	// in progress — same reasoning as Campaigns (see ReplayBusy).
	Replays ReplayBusy
	Grace   time.Duration
	Log     *slog.Logger
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
	// ReadyTimeout bounds how long waitReadyOrFail waits, per launch,
	// for Ready() to report true before giving up (P3-14). Defaults to
	// defaultReadyTimeout (30s, today's hardcoded value) when unset.
	// Exceeding it is NOT a failure (P2-2): the state stays "restarting"
	// and Run's own readiness ticker keeps checking in the background.
	ReadyTimeout time.Duration
	// EndPaperSession closes the outgoing paper session row (E10) after
	// a restart drains it. Optional: nil when persistence is off.
	EndPaperSession func(ctx context.Context, sessionID string, at time.Time) error
	// endSessionOnRestart is the pre-resumption behaviour (every restart
	// ends the outgoing session); tests of that path set it. Production
	// leaves it false so the next run resumes the session (P1-6).
	endSessionOnRestart bool
	// SessionID returns the engine's current paper session id (input to
	// EndPaperSession). Optional; only consulted when EndPaperSession is set.
	SessionID func() string
	// SetPaperPaused, when set, is called immediately before every
	// restart-driven launch with the pause decision the JUST-CAPTURED
	// paper engine's Running() implied (P2-2): true to start the new run
	// paused, false to start it running. Consumed by the engine at the
	// paperEng.Resume() boot site instead of the pre-fix shape, where the
	// supervisor could only re-Pause() AFTER Engine.Run had already
	// reached readiness — which cannot work during a slow bootstrap
	// (ready-timeout): s.Paper() returns nil until the new paper engine
	// object exists, so "re-pause after the fact" has nothing to act on.
	// Optional; a caller that omits it keeps the engine's own unconditional
	// boot-time Resume() (today's behavior for direct-constructed engines
	// and tests).
	SetPaperPaused func(paused bool)

	initOnce sync.Once
	reqs     chan RestartRequest
	errCh    chan runExit
	// genCounter (P1-1) hands out the generation id every launch tags its
	// exit with. Only ever touched from Supervisor.Run's own goroutine
	// (launch is called exclusively from Run or from restart(), which
	// Run calls synchronously) — atomic regardless, cheap insurance
	// against a future caller breaking that invariant.
	genCounter atomic.Uint64

	subscribedOnce sync.Once

	mu              sync.Mutex
	st              RestartStatus
	runningSettings platform.Settings
	pendingPlatform string
	pendingWorkers  string
	// pendingGen (P1-1/P2-6) is non-zero while a generation has been
	// cancelled (a restart's step 4) but its exit has not yet been
	// observed on errCh — a grace timeout, specifically. Request refuses
	// a NEW restart while this is set, which is what keeps at most one
	// abandoned generation ever outstanding at a time: errCh's buffer
	// size relies on that bound.
	pendingGen uint64
}

func (s *Supervisor) Name() string { return "engine-supervisor" }

func (s *Supervisor) init() {
	s.initOnce.Do(func() {
		s.reqs = make(chan RestartRequest, 1)
		// Buffered 4: at most one abandoned generation (pendingGen gate)
		// plus one live generation can ever be outstanding at a time, so
		// 2 would provably suffice — 4 is slack, not a required minimum.
		s.errCh = make(chan runExit, 4)
		if s.Log == nil {
			s.Log = slog.New(slog.DiscardHandler)
		}
		if s.Grace <= 0 {
			s.Grace = 15 * time.Second
		}
	})
}

func (s *Supervisor) readyTimeout() time.Duration {
	if s.ReadyTimeout > 0 {
		return s.ReadyTimeout
	}
	return defaultReadyTimeout
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
	if s.Replays != nil {
		if id, busy := s.Replays.BusyRun(); busy {
			return fmt.Errorf("%w: replay run %s is in progress", ErrReplayRunning, id)
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
	if s.pendingGen != 0 {
		// P1-1: a previous restart's cancel-and-wait step timed out and
		// that generation's exit has not been observed yet — launching
		// another one now is exactly the bug this field exists to
		// prevent (two Engine.Run calls alive at once).
		s.mu.Unlock()
		return fmt.Errorf("%w: waiting for a previous run to fully stop", ErrRestartInProgress)
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
		// defensive rather than block the caller forever — and, having
		// already flipped state to Restarting above, unwind it (P3-2)
		// so this defensive refusal does not leave the state machine
		// stuck in "restarting" with nobody left to move it.
		s.abortRestart()
		return ErrRestartInProgress
	}
}

// abortRestart restores whichever RestartStatus Request() had flipped
// FROM (Ready or Pending, per the currently pending reasons) when a
// restart is refused AFTER that flip already happened: the recorder
// re-check inside restart() (P2-6, running the TOCTOU-safe check a
// second time, INSIDE the single-threaded restart loop) and Request's
// own defensive channel-send guard (P3-2) both use this. Must not touch
// Restarts/ReadyAt/SettingsVersion/Error: nothing about the running
// engine changed.
func (s *Supervisor) abortRestart() {
	s.mu.Lock()
	if len(s.st.PendingReasons) > 0 {
		s.st.State = StatePending
	} else {
		s.st.State = StateReady
	}
	snapshot := s.st
	s.mu.Unlock()
	if s.OnState != nil {
		s.OnState(snapshot)
	}
}

// observeGenExit clears pendingGen when it matches gen — called for
// EVERY exit read off errCh, whether or not it belongs to the
// currently-tracked live generation, since pendingGen tracks a
// DIFFERENT (abandoned) generation than curGen once a grace timeout has
// happened.
func (s *Supervisor) observeGenExit(gen uint64) {
	s.mu.Lock()
	if s.pendingGen == gen {
		s.pendingGen = 0
	}
	s.mu.Unlock()
}

// Run starts the engine and services restart requests until ctx cancels.
func (s *Supervisor) Run(ctx context.Context) error {
	s.init()

	snap := s.Settings.Current()
	cancelEngine, curGen := s.launch(ctx, snap)
	engineAlive := true

	// awaiting* track a launch (boot or restart) whose readiness has not
	// been confirmed yet, either because it just succeeded synchronously
	// (awaitingGen == 0, nothing to track) or because it hit
	// ReadyTimeout while still alive (P2-2): the ticker in the loop
	// below keeps polling Ready() until it flips, or the generation's
	// exit arrives through the normal errCh case.
	var (
		awaitingGen       uint64
		awaitingVersion   int64
		awaitingActor     string
		awaitingIsRestart bool
	)

	readyErr := s.waitReadyOrFail(curGen, s.readyTimeout())
	switch {
	case readyErr == nil:
		// P3-1: the initial boot is not a restart — markReady (state
		// transition only) without bumping Restarts.
		s.markReady(snap.Version)
	case errors.Is(readyErr, errReadyTimeout):
		awaitingGen, awaitingVersion, awaitingIsRestart = curGen, snap.Version, false
	default:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Restarts is necessarily 0 here (markReady has not run yet):
		// today's fail-fast boot semantics, preserved exactly.
		return readyErr
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

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if engineAlive {
				cancelEngine()
				s.waitEngineStop(curGen, s.Grace)
			}
			return ctx.Err()

		case exit := <-s.errCh:
			s.observeGenExit(exit.gen)
			if exit.gen != curGen {
				// A stale exit from an abandoned generation (pendingGen)
				// arriving late — must never be attributed to whichever
				// generation happens to be live right now (P1-1).
				continue
			}
			engineAlive = false
			awaitingGen = 0
			if exit.err != nil && !errors.Is(exit.err, context.Canceled) {
				if s.Status().Restarts == 0 {
					// Today's fail-fast boot semantics, preserved
					// exactly (P3-1 makes this reachable again: Restarts
					// no longer counts the boot itself).
					return exit.err
				}
				s.markFailed(exit.err)
			}

		case <-ticker.C:
			if awaitingGen != 0 && awaitingGen == curGen && (s.Ready == nil || s.Ready()) {
				if awaitingIsRestart {
					s.completeRestart(awaitingVersion, awaitingActor)
				} else {
					s.markReady(awaitingVersion)
				}
				awaitingGen = 0
			}

		case req := <-s.reqs:
			newCancel, newGen, version, awaiting := s.restart(ctx, req, cancelEngine, curGen, engineAlive)
			cancelEngine = newCancel
			if newGen != 0 {
				curGen = newGen
				engineAlive = true
				if awaiting {
					awaitingGen, awaitingVersion, awaitingActor, awaitingIsRestart = newGen, version, req.Actor, true
				} else {
					awaitingGen = 0
				}
			} else {
				engineAlive = false
				awaitingGen = 0
			}
		}
	}
}

// launch starts the engine against snap and returns its cancel func and
// the generation (P1-1) this launch's eventual exit will be tagged with.
func (s *Supervisor) launch(parent context.Context, snap platform.Snapshot) (context.CancelFunc, uint64) {
	s.startRun(snap.Settings, snap.Version)
	gen := s.genCounter.Add(1)
	ctx, cancel := context.WithCancel(parent)
	go func() { s.errCh <- runExit{gen: gen, err: s.Engine.Run(ctx)} }()
	return cancel, gen
}

// restart executes design §2.4 steps 2-5. curGen/engineAlive describe
// the run in flight when the request arrived; when engineAlive is false
// (e.g. a prior failure left the supervisor idle) the cancel-and-wait
// step is skipped entirely.
//
// Returns the cancel func to track going forward (newCancel), the
// generation it belongs to (newGen — 0 means no new generation was
// launched: the caller must leave curGen/engineAlive exactly as they
// were), the settings version that generation is running (meaningful
// only when newGen != 0), and whether that generation is still awaiting
// its OWN readiness confirmation (P2-2: true only on a ready-timeout,
// where Run's ticker takes over polling).
func (s *Supervisor) restart(parent context.Context, req RestartRequest, cancelCurrent context.CancelFunc, curGen uint64, engineAlive bool) (newCancel context.CancelFunc, newGen uint64, version int64, awaitingReady bool) {
	done := func(err error) {
		if req.Done != nil {
			req.Done <- err
			close(req.Done)
		}
	}

	// P2-6: re-check the recording guard INSIDE the single-threaded
	// restart loop, not only once at Request() time — a session can
	// start in the window between Request() enqueuing this and the loop
	// actually reaching it. Request() already flipped state to
	// StateRestarting; a refusal here must unwind that (abortRestart,
	// P3-2's fix applied to this new code path too) rather than leave it
	// stuck.
	if !req.StopRecording && s.Recorder != nil {
		if rc := s.Recorder(); rc != nil {
			if st := rc.Status(); st.Running {
				err := fmt.Errorf("%w: recording session %s is active — stop it, or resend with stop_recording", ErrRecordingActive, st.SessionID)
				s.abortRestart()
				done(err)
				return cancelCurrent, 0, 0, false
			}
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
	var pe *paper.Engine
	var wasRunning bool
	if s.Paper != nil {
		if p := s.Paper(); p != nil {
			pe = p
			wasRunning = pe.Running()
			pe.Pause()
		}
	}
	// P2-3: every early return below must restore whatever pause state
	// THIS attempt disturbed on the object captured above — otherwise a
	// LATER restart's OWN step-3 capture (this same code, next time
	// around) reads a stale, permanently-paused object (this one was
	// abandoned, e.g. by a grace timeout, and nothing else will ever
	// touch it again) and never recovers a paper engine that was
	// genuinely running before this attempt. Cleared (committed = true)
	// once a NEW generation actually launches: from that point on,
	// SetPaperPaused plus the new engine's own boot sequence own the
	// pause state, not this object.
	committed := false
	defer func() {
		if !committed && wasRunning && pe != nil {
			pe.Resume()
		}
	}()

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
		if !s.waitEngineStop(curGen, s.Grace) {
			// P1-1: never discard a live cancel and never lose track of
			// this generation — the old run may still be executing
			// (grace elapsed; that does not mean it has returned).
			// pendingGen gates Request() until this generation's exit is
			// actually observed, so a second restart can never launch
			// while this one might still be running.
			s.mu.Lock()
			s.pendingGen = curGen
			s.mu.Unlock()
			s.markFailed(fmt.Errorf("engine: restart timed out waiting for the previous run to stop (grace %s)", s.Grace))
			done(errors.New("restart timed out"))
			return cancelCurrent, 0, 0, false
		}
	}
	// The outgoing paper session is NOT ended here any more: a restart
	// resumes it — same session id, ledger continued from its last
	// snapshot — unless the new settings change the starting balances,
	// in which case the engine's own resumption step ends it (P1-6).
	// Ending it on every restart is what made every restart a fresh
	// ledger. EndPaperSession stays wired for the supersede path only.
	if s.EndPaperSession != nil && outgoingSession != "" && s.endSessionOnRestart {
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.EndPaperSession(endCtx, outgoingSession, time.Now()); err != nil {
			s.Log.Warn("end paper session failed", "session", outgoingSession, "error", err)
		}
		cancel()
	}

	// Step 5: re-enter. Carry the pause decision INTO the engine (P2-2)
	// before launching: s.Paper() can return nil for a while during a
	// slow bootstrap, so re-pausing AFTER the fact (the pre-fix shape)
	// has nothing to act on during that window. SetPaperPaused is
	// consumed once, by the engine's own PAPER-mode assembly, regardless
	// of how long readiness takes.
	if s.SetPaperPaused != nil {
		s.SetPaperPaused(!wasRunning)
	}
	snap := s.Settings.Current()
	launchedCancel, gen := s.launch(parent, snap)
	readyErr := s.waitReadyOrFail(gen, s.readyTimeout())
	switch {
	case readyErr == nil:
		committed = true
		if s.SetPaperPaused == nil && !wasRunning && pe != nil {
			// Fallback for a caller that never wires SetPaperPaused (a
			// minimal Supervisor in a test, or a future caller that only
			// needs the synchronous-readiness case): re-apply the pause
			// decision here, now that the new run has confirmed ready
			// and s.Paper() can be trusted to return its object. This is
			// exactly the pre-P2-2 mechanism, kept ONLY as a fallback —
			// production wiring (components.go) always sets
			// SetPaperPaused, which is what makes the SLOW-bootstrap
			// case (readyErr == errReadyTimeout, below) correct; this
			// fallback does not cover that case.
			if p := s.Paper(); p != nil {
				p.Pause()
			}
		}
		s.completeRestart(snap.Version, req.Actor)
		done(nil)
		return launchedCancel, gen, snap.Version, false
	case errors.Is(readyErr, errReadyTimeout):
		// P2-2: still bootstrapping past the deadline — not a failure.
		// Report the request as accepted (nil): Run's readiness ticker
		// confirms it later (completeRestart, including the Restarts
		// bump and the "engine.restart.completed" audit) once Ready()
		// flips, or the generation's own exit arrives.
		committed = true
		done(nil)
		return launchedCancel, gen, snap.Version, true
	case parent.Err() != nil:
		// Parent shutting down concurrently with the restart: not a
		// restart failure, let the outer Run loop's ctx.Done() case take
		// over on its next iteration — report this generation so that
		// branch cancels+waits for the RIGHT one.
		committed = true
		done(nil)
		return launchedCancel, gen, snap.Version, false
	default:
		s.markFailed(fmt.Errorf("engine: restart failed: %w", readyErr))
		done(fmt.Errorf("restart failed: %w", readyErr))
		return func() {}, 0, 0, false
	}
}

// waitEngineStop waits up to grace for generation gen's exit to appear
// on errCh, discarding its error value (the caller has already decided
// how to react to a fatal error via the normal errCh case; this path
// exists only to guarantee sequencing before re-entry). Any OTHER
// generation's exit encountered while waiting is observed (P1-1: clears
// pendingGen if it matches) and skipped — it is not what this call is
// waiting for. Returns false on timeout.
func (s *Supervisor) waitEngineStop(gen uint64, grace time.Duration) bool {
	deadline := time.After(grace)
	for {
		select {
		case exit := <-s.errCh:
			s.observeGenExit(exit.gen)
			if exit.gen != gen {
				continue
			}
			return true
		case <-deadline:
			return false
		}
	}
}

// waitReadyOrFail polls Ready (when set) so state can flip to ready on
// the engine's own bootstrap completion rather than merely "the
// goroutine has not errored yet" — and watches s.errCh at the same time,
// so an engine that fails immediately (e.g. a bad settings document a
// dry-run should have caught, but didn't) is detected within one poll
// interval instead of only after the full deadline. Returns:
//   - nil, once Ready() reports true (or immediately, when Ready is
//     unset — launch is assumed successful once the goroutine is
//     running);
//   - the error generation gen exited with, if it exited before
//     becoming ready;
//   - errReadyTimeout (P2-2) if deadline elapses with the generation
//     still alive and still not ready — NOT treated as a failure by
//     callers.
//
// Any OTHER generation's exit encountered while waiting is observed
// (P1-1: clears pendingGen if it matches) and skipped.
func (s *Supervisor) waitReadyOrFail(gen uint64, deadline time.Duration) error {
	checkExit := func() (err error, resolved bool) {
		select {
		case exit := <-s.errCh:
			s.observeGenExit(exit.gen)
			if exit.gen != gen {
				return nil, false
			}
			return exit.err, true
		default:
			return nil, false
		}
	}
	if err, resolved := checkExit(); resolved {
		return err
	}
	if s.Ready == nil || s.Ready() {
		return nil
	}
	cutoff := time.After(deadline)
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()
	for {
		if err, resolved := checkExit(); resolved {
			return err
		}
		select {
		case <-cutoff:
			return errReadyTimeout
		case <-ticker.C:
			if s.Ready() {
				return nil
			}
		}
	}
}

func (s *Supervisor) startRun(settings platform.Settings, version int64) {
	s.Engine.ApplySettings(settings, version)
	s.mu.Lock()
	s.runningSettings = settings.Clone()
	s.mu.Unlock()
}

// markReady flips the state to Ready. It intentionally does NOT bump
// Restarts (P3-1): it runs on the initial boot too, and the initial
// boot is not a restart. completeRestart (below) is markReady plus the
// bookkeeping that only applies to an ACTUAL restart.
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
		st.Error = ""
	})
	s.recomputePending()
}

// completeRestart marks a restart-driven launch ready, incrementing
// Restarts (P3-1: only an ACTUAL restart counts — never the initial
// boot) and auditing "engine.restart.completed" (P3-9, distinct from
// the API layer's "engine.restart.requested"). Called both from
// restart() itself (the common case: readiness confirmed synchronously,
// well inside ReadyTimeout) and from Run's readiness ticker (P2-2: a
// restart whose bootstrap took longer than ReadyTimeout, confirmed
// later, asynchronously) — either way this is the ONE place a restart is
// considered to have actually finished.
func (s *Supervisor) completeRestart(version int64, actor string) {
	s.markReady(version)
	s.mu.Lock()
	s.st.Restarts++
	snapshot := s.st
	s.mu.Unlock()
	if s.OnState != nil {
		s.OnState(snapshot)
	}
	if s.Audit != nil {
		s.Audit(actor, "engine.restart.completed", "engine")
	}
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
					// T-059 §2.4: a mode change names the transition so the
					// banner can say what the restart will do; other
					// restart-scoped fields keep the generic reason.
					s.pendingPlatform = fmt.Sprintf("platform settings v%d", newSnap.Version)
					if from, to := running.Platform.Mode, newSnap.Settings.Platform.Mode; from != to {
						s.pendingPlatform = fmt.Sprintf("platform settings v%d: mode %s→%s", newSnap.Version, from, to)
					}
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
			// P3-3: PendingVersion exists to tell the operator WHICH
			// newer version is waiting on a restart; once the last
			// pending reason clears (a restart happened, or the change
			// was rolled back) there is nothing left to point at.
			s.st.PendingVersion = 0
		}
	}
	snapshot := s.st
	s.mu.Unlock()
	if s.OnState != nil {
		s.OnState(snapshot)
	}
}
