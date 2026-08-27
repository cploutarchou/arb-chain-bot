package api

import (
	"net/http"
	"runtime"
	"time"
)

// handleSystemHealth assembles the full BL-18 payload from every source
// that is actually present in this profile — engine-derived sections
// (feed/books/paper/queues.outbox/queues.paper) come from s.Reads when
// set; process stats, DB pool stats, the recorder's queue depth, and
// supervisor restart state are all independent of the engine and are
// included whenever their own component is present. Absent sources are
// simply omitted, never faked with zeros.
func (s *Server) handleSystemHealth(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"process": s.processStats(),
	}
	if s.Reads != nil {
		mergeInto(out, s.Reads.Health())
	}
	if s.Store != nil {
		out["database"] = s.Store.PoolStats()
	}
	if s.Recorder != nil {
		rs := s.Recorder.Status()
		queues, _ := out["queues"].(map[string]any)
		if queues == nil {
			queues = map[string]any{}
		}
		queues["recorder"] = map[string]any{"depth": rs.QueueDepth, "capacity": rs.QueueCapacity}
		out["queues"] = queues
	}
	if s.Restart != nil {
		out["restart"] = s.Restart.Status()
	}
	WriteData(w, http.StatusOK, out)
}

// mergeInto copies a map[string]any's top-level keys into dst (the
// engine's Health() shape); anything else (a fake ReadModel in a test
// that returns a struct, say) is nested under "engine" so it is never
// silently dropped.
func mergeInto(dst map[string]any, v any) {
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			dst[k] = val
		}
		return
	}
	dst["engine"] = v
}

// processStats reports this process's own resource use (BL-18): uptime,
// goroutines, heap/sys memory, and cumulative GC pause time — the panel
// the console needs to answer "is the backend itself healthy" even when
// every downstream component (DB, exchange feed) is fine.
type processStatsView struct {
	UptimeSeconds  int64  `json:"uptime_sec"`
	Goroutines     int    `json:"goroutines"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes   uint64 `json:"heap_sys_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	GCPauseTotalNs uint64 `json:"gc_pause_total_ns"`
	NumGC          uint32 `json:"num_gc"`
}

func (s *Server) processStats() processStatsView {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return processStatsView{
		UptimeSeconds:  int64(time.Since(s.start).Seconds()),
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocBytes: m.HeapAlloc,
		HeapSysBytes:   m.HeapSys,
		SysBytes:       m.Sys,
		GCPauseTotalNs: m.PauseTotalNs,
		NumGC:          m.NumGC,
	}
}
