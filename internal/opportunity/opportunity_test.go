package opportunity

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func quoteFixture() pricing.CycleQuote {
	q := pricing.CycleQuote{
		Triangle:      "binance|USDT|BTCUSDT>ETHBTC>ETHUSDT",
		Start:         "USDT",
		InputConsumed: d("1000"),
		FinalAmount:   d("1016.94204"),
	}
	q.GrossProfit = q.FinalAmount.Sub(q.InputConsumed)
	q.ReturnBps = d("169.4204")
	q.Legs[0].BookVersion = 11
	q.Legs[1].BookVersion = 22
	q.Legs[2].BookVersion = 33
	return q
}

// Buffer math, hand-computed: input 1000, buffers 5+10=15 bps → 1.5 USDT.
// estimated final 1015.44204; net profit 15.44204; net 154.4204 bps.
func TestBuildBufferedEconomics(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	o := Build("op1", "binance", quoteFixture(),
		Buffers{LatencyBps: d("5"), RiskBps: d("10")}, 500*time.Millisecond, now, 42)

	if !o.BufferAmount.Equal(d("1.5")) {
		t.Fatalf("BufferAmount = %s", o.BufferAmount)
	}
	if !o.EstimatedFinal.Equal(d("1015.44204")) {
		t.Fatalf("EstimatedFinal = %s", o.EstimatedFinal)
	}
	if !o.NetProfit.Equal(d("15.44204")) {
		t.Fatalf("NetProfit = %s", o.NetProfit)
	}
	if !o.NetReturnBps.Equal(d("154.4204")) {
		t.Fatalf("NetReturnBps = %s", o.NetReturnBps)
	}
	if o.Status != StatusDetected || o.ConfigVersion != 42 {
		t.Fatalf("status=%s cfg=%d", o.Status, o.ConfigVersion)
	}
	if !o.ExpiresAt.Equal(now.Add(500 * time.Millisecond)) {
		t.Fatalf("ExpiresAt = %s", o.ExpiresAt)
	}
}

func TestLifecycleLegalPath(t *testing.T) {
	o := Build("op", "binance", quoteFixture(), Buffers{}, time.Second, time.Now(), 1)
	for _, step := range []Status{StatusCalculating, StatusQualified, StatusReserved, StatusSimulating, StatusCompleted} {
		if err := o.Transition(step, ""); err != nil {
			t.Fatalf("to %s: %v", step, err)
		}
	}
	// Terminal: nothing further.
	if err := o.Transition(StatusFailed, ""); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("terminal transition allowed: %v", err)
	}
}

func TestIllegalTransitions(t *testing.T) {
	o := Build("op", "binance", quoteFixture(), Buffers{}, time.Second, time.Now(), 1)
	if err := o.Transition(StatusSimulating, ""); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("DETECTED->SIMULATING allowed: %v", err)
	}
	_ = o.Transition(StatusCalculating, "")
	if err := o.Transition(StatusReserved, ""); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("CALCULATING->RESERVED allowed: %v", err)
	}
}

func TestRejectedCarriesReason(t *testing.T) {
	o := Build("op", "binance", quoteFixture(), Buffers{}, time.Second, time.Now(), 1)
	_ = o.Transition(StatusCalculating, "")
	if err := o.Transition(StatusRejected, "RISK_MIN_EDGE"); err != nil {
		t.Fatal(err)
	}
	if o.Reason != "RISK_MIN_EDGE" {
		t.Fatalf("reason = %s", o.Reason)
	}
}

func TestExpiryRules(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	o := Build("op", "binance", quoteFixture(), Buffers{}, time.Second, now, 1)
	_ = o.Transition(StatusCalculating, "")
	_ = o.Transition(StatusQualified, "")

	// Not yet expired.
	if err := o.Expire(now.Add(500 * time.Millisecond)); err == nil {
		t.Fatal("expired early")
	}
	if err := o.Expire(now.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusExpired || o.Reason != "TTL_EXPIRED" {
		t.Fatalf("status=%s reason=%s", o.Status, o.Reason)
	}

	// A COMPLETED opportunity cannot expire.
	o2 := Build("op2", "binance", quoteFixture(), Buffers{}, time.Nanosecond, now, 1)
	_ = o2.Transition(StatusCalculating, "")
	_ = o2.Transition(StatusQualified, "")
	_ = o2.Transition(StatusReserved, "")
	_ = o2.Transition(StatusSimulating, "")
	_ = o2.Transition(StatusCompleted, "")
	if err := o2.Expire(now.Add(time.Hour)); !errors.Is(err, ErrNotExpirable) {
		t.Fatalf("terminal expiry: %v", err)
	}
}

func TestRevalidationContract(t *testing.T) {
	o := Build("op", "binance", quoteFixture(), Buffers{}, time.Second, time.Now(), 1)
	if o.NeedsRecalc([3]uint64{11, 22, 33}) {
		t.Fatal("unchanged versions must not trigger recalc")
	}
	if !o.NeedsRecalc([3]uint64{11, 23, 33}) {
		t.Fatal("any moved leg version must trigger recalc")
	}
}
