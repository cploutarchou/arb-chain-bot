package venue

import (
	"errors"
	"testing"
	"time"
)

// HTX answers HTTP 200 with status="error" when it refuses for rate: the
// soak on 2026-08-28 recorded err-code "invalid-parameter", err-msg
// "request limit" on batch_merged and the poller kept polling because the
// envelope was a plain error. It must now back the gate off.
func TestHTXRequestLimitEnvelopeBacksOffTheGate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := &gate{now: func() time.Time { return now }}

	env := htxStatus{Status: "error", ErrCode: "invalid-parameter", ErrMsg: "request limit"}
	err := env.check("batch_merged", g)
	var ib *InBandRateLimit
	if !errors.As(err, &ib) {
		t.Fatalf("check() = %v, want an *InBandRateLimit", err)
	}
	if got := g.blockedFor(); got < htxInBandPause-time.Second || got > htxInBandPause+time.Second {
		t.Fatalf("gate blocked for %v, want about %v", got, htxInBandPause)
	}
	if g.limited.Load() != 1 {
		t.Fatalf("RateLimited() = %d, want 1", g.limited.Load())
	}

	// A non-rate error stays a plain error and never pauses the gate.
	g2 := &gate{now: func() time.Time { return now }}
	other := htxStatus{Status: "error", ErrCode: "invalid-parameter", ErrMsg: "symbol not found"}
	if err := other.check("market/tickers", g2); err == nil || errors.As(err, &ib) {
		t.Fatalf("non-rate envelope = %v, want a plain error", err)
	}
	if g2.blockedFor() != 0 || g2.limited.Load() != 0 {
		t.Fatalf("non-rate envelope moved the gate: blocked=%v limited=%d", g2.blockedFor(), g2.limited.Load())
	}
}
