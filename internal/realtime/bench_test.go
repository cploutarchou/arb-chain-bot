package realtime

import (
	"encoding/json"
	"testing"
)

// WebSocket fan-out (§73): one Publish delivered to 100 subscribed
// sinks, drained concurrently so buffers never saturate.
func BenchmarkPublishFanout100(b *testing.B) {
	hub := NewHub(1024)
	hub.RegisterTopic("scanner", func() (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	const clients = 100
	done := make(chan struct{})
	for i := 0; i < clients; i++ {
		sink := hub.Attach()
		if err := hub.Subscribe(sink, "scanner"); err != nil {
			b.Fatal(err)
		}
		go func(s *Sink) {
			for range s.Ch() {
			}
			done <- struct{}{}
		}(sink)
	}
	payload := map[string]any{"opportunity_id": "op-1", "net_bps": "17.42"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := hub.Publish("scanner", payload); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}
