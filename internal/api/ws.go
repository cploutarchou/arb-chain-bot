package api

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// handleWS upgrades an authenticated session to the realtime hub. The
// same-origin check admits only the configured console origin (dev) or
// same-host requests; everything else is refused before upgrade.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.Hub == nil {
		WriteError(w, http.StatusNotFound, "realtime_absent", "hub not running", correlationID(r))
		return
	}
	up := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // non-browser client with a valid session cookie
			}
			return origin == s.cfg.AllowedOrigin || origin == "https://"+r.Host || origin == "http://"+r.Host
		},
	}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error
	}
	sink := s.Hub.Attach()
	defer s.Hub.Detach(sink)
	defer func() { _ = conn.Close() }()

	done := make(chan struct{})
	// Read loop: client subscribe/unsubscribe ops.
	go func() {
		defer close(done)
		conn.SetReadLimit(1 << 16)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = s.Hub.HandleClientOp(r.Context(), sink, raw)
		}
	}()

	// Write pump: drain the sink; on empty queue, recover lagged topics;
	// periodic pings keep intermediaries open.
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case msg, ok := <-sink.Ch():
			if !ok {
				return
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
			if len(sink.Ch()) == 0 {
				_ = s.Hub.RecoverIfLagged(sink)
			}
		}
	}
}
