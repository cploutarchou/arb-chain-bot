package app

import (
	"sort"
	"sync"
	"time"
)

// latencyWindow is a small fixed-capacity rolling sample of per-exchange
// message latencies (ms), read by GET /api/v1/system/health for
// percentiles (BL-18). It exists because the OTel histogram Metrics
// already records into is write-only from this process's point of view
// (no in-process read-back without standing up an exporter); this ring
// buffer is the queryable side-channel. Record is O(1) under a plain
// mutex — cheap enough for the message-latency observer's call rate,
// and it touches only memory (no DB/network/logging), so it does not
// violate the hot-path rule.
type latencyWindow struct {
	mu     sync.Mutex
	buf    [256]float64
	filled int
	next   int
}

func (w *latencyWindow) Record(ms float64) {
	w.mu.Lock()
	w.buf[w.next] = ms
	w.next = (w.next + 1) % len(w.buf)
	if w.filled < len(w.buf) {
		w.filled++
	}
	w.mu.Unlock()
}

// LatencySnapshot is the percentile/sample-count view; N is always
// reported alongside the percentiles (every BL-19/BL-18 aggregate does).
type LatencySnapshot struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
}

func (w *latencyWindow) Snapshot() LatencySnapshot {
	w.mu.Lock()
	n := w.filled
	samples := make([]float64, n)
	copy(samples, w.buf[:n])
	w.mu.Unlock()
	if n == 0 {
		return LatencySnapshot{}
	}
	sort.Float64s(samples)
	pick := func(p float64) float64 {
		idx := int(p * float64(n-1))
		return samples[idx]
	}
	return LatencySnapshot{N: n, P50: pick(0.50), P95: pick(0.95), P99: pick(0.99)}
}

// rateSampler turns a cumulative counter into a per-second rate by
// delta-sampling across calls (BL-18: message rates without adding a
// hot-path timer). The first call after construction/reset has no prior
// sample and reports zero, honestly, rather than a nonsensical spike.
type rateSampler struct {
	mu      sync.Mutex
	lastAt  time.Time
	lastVal int64
}

func (s *rateSampler) rate(now time.Time, val int64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r float64
	if !s.lastAt.IsZero() {
		if dt := now.Sub(s.lastAt).Seconds(); dt > 0 {
			r = float64(val-s.lastVal) / dt
		}
	}
	s.lastAt, s.lastVal = now, val
	return r
}
