package api

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
)

// wsTopicPerms maps every registered hub topic to the permission its
// HTTP equivalent requires (audit S11): the socket must not become the
// RBAC bypass that walks around the REST routes' requirePerm gates.
// Unknown topics fail closed — an unregistered name has no permission
// to inherit.
var wsTopicPerms = map[realtime.Topic]auth.Permission{
	"scanner":    auth.PermViewDashboard,
	"alerts":     auth.PermViewDashboard,
	"health":     auth.PermViewSystem,
	"recordings": auth.PermViewSystem,
	"campaigns":  auth.PermViewSystem,
	"replays":    auth.PermViewSystem,
}

// wsAllow builds the per-connection authorization from the principal
// the session middleware already resolved.
func wsAllow(p *Principal) func(realtime.Topic) bool {
	return func(t realtime.Topic) bool {
		perm, ok := wsTopicPerms[t]
		if !ok {
			return false
		}
		return auth.Can(p.Role, perm)
	}
}

// handleWS upgrades an authenticated session to the realtime hub. The
// same-origin check admits only the configured console origin (dev) or
// same-host requests; everything else is refused before upgrade.
// Subscribes are authorized per topic against the session's role: an
// unauthorised subscribe is answered with an error frame, never data.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.Hub == nil {
		WriteError(w, http.StatusNotFound, "realtime_absent", "hub not running", correlationID(r))
		return
	}
	// Captured before the read loop: the request context outlives the
	// handler body only through this closure's usage, and the principal
	// values themselves are immutable.
	principal, _ := PrincipalFrom(r.Context())
	up := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // non-browser client with a valid session cookie
			}
			// platform.allowed_origin is hot (D7); the same-host clauses are
			// unconditional so a bad value can never lock a same-origin
			// console out.
			return origin == s.AllowedOrigin() || origin == "https://"+r.Host || origin == "http://"+r.Host
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
			_ = s.Hub.HandleClientOp(r.Context(), sink, raw, wsAllow(&principal))
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
