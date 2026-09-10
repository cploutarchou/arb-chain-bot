// Package realtime implements the topic-based streaming hub
// (docs/architecture.md §8): subscribe → snapshot, then seq-numbered
// diffs; a lagging client's queue is dropped and its next frame is a
// fresh snapshot flagged resync. The hub core is transport-agnostic and
// fully tested; the WebSocket shell adapts it to gorilla conns.
package realtime

import (
	"context"
	"encoding/json"
	"sync"
)

// Topic names one stream (scanner, health, cycles, pnl, alerts, …).
type Topic string

// Message is the wire frame. Error is set only on authorization
// rejections (audit S11): a subscribe the caller's role does not permit
// is answered with one error frame per refused topic instead of a
// silent no-op, so a client never mistakes "not allowed" for "no data".
type Message struct {
	Topic    Topic           `json:"topic"`
	Seq      uint64          `json:"seq"`
	Snapshot bool            `json:"snapshot,omitempty"`
	Resync   bool            `json:"resync,omitempty"`
	Error    string          `json:"error,omitempty"`
	Data     json.RawMessage `json:"data"`
}

// SnapshotFunc produces the current full state of a topic. Producers
// register one per topic; it runs outside the hot path.
type SnapshotFunc func() (json.RawMessage, error)

// Sink is one client connection's send surface: a bounded queue drained
// by the transport's write pump.
type Sink struct {
	hub    *Hub
	ch     chan Message
	mu     sync.Mutex
	subs   map[Topic]bool
	lagged map[Topic]bool
	closed bool
}

// Ch is the transport's read side of the queue.
func (s *Sink) Ch() <-chan Message { return s.ch }

// Hub routes published diffs to subscribed sinks.
type Hub struct {
	mu        sync.RWMutex
	topics    map[Topic]*topicState
	sinks     map[*Sink]struct{}
	queueSize int
}

type topicState struct {
	seq      uint64
	snapshot SnapshotFunc
}

func NewHub(queueSize int) *Hub {
	if queueSize <= 0 {
		queueSize = 64
	}
	return &Hub{
		topics:    make(map[Topic]*topicState),
		sinks:     make(map[*Sink]struct{}),
		queueSize: queueSize,
	}
}

// RegisterTopic declares a topic and its snapshot provider.
func (h *Hub) RegisterTopic(t Topic, snap SnapshotFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.topics[t] = &topicState{snapshot: snap}
}

// Attach creates a sink for a new client.
func (h *Hub) Attach() *Sink {
	s := &Sink{
		hub:    h,
		ch:     make(chan Message, h.queueSize),
		subs:   make(map[Topic]bool),
		lagged: make(map[Topic]bool),
	}
	h.mu.Lock()
	h.sinks[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Detach removes the sink and closes its queue.
func (h *Hub) Detach(s *Sink) {
	h.mu.Lock()
	delete(h.sinks, s)
	h.mu.Unlock()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	s.mu.Unlock()
}

// Subscribe adds topics to a sink and enqueues immediate snapshots.
func (h *Hub) Subscribe(s *Sink, topics ...Topic) error {
	for _, t := range topics {
		h.mu.RLock()
		st, ok := h.topics[t]
		h.mu.RUnlock()
		if !ok {
			continue // unknown topics are ignored, not errors (forward compat)
		}
		s.mu.Lock()
		s.subs[t] = true
		s.mu.Unlock()
		if err := h.sendSnapshot(s, t, st, false); err != nil {
			return err
		}
	}
	return nil
}

// Unsubscribe removes topics.
func (h *Hub) Unsubscribe(s *Sink, topics ...Topic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range topics {
		delete(s.subs, t)
		delete(s.lagged, t)
	}
}

// Publish increments the topic seq and fans the diff out. A full sink is
// never blocked on: the client is marked lagged, pending messages stay
// (transport drains them), and recovery happens on its next drain via
// RecoverIfLagged.
func (h *Hub) Publish(t Topic, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	h.mu.Lock()
	st, ok := h.topics[t]
	if !ok {
		h.mu.Unlock()
		return nil
	}
	st.seq++
	seq := st.seq
	sinks := make([]*Sink, 0, len(h.sinks))
	for s := range h.sinks {
		sinks = append(sinks, s)
	}
	h.mu.Unlock()

	msg := Message{Topic: t, Seq: seq, Data: raw}
	for _, s := range sinks {
		s.mu.Lock()
		if s.closed || !s.subs[t] || s.lagged[t] {
			s.mu.Unlock()
			continue
		}
		select {
		case s.ch <- msg:
		default:
			// Queue full: drop diffs for this topic until resync.
			s.lagged[t] = true
		}
		s.mu.Unlock()
	}
	return nil
}

// RecoverIfLagged re-snapshots every lagged topic of a sink; the
// transport calls it when the client's queue has drained.
func (h *Hub) RecoverIfLagged(s *Sink) error {
	s.mu.Lock()
	var lagged []Topic
	for t := range s.lagged {
		lagged = append(lagged, t)
	}
	s.mu.Unlock()
	for _, t := range lagged {
		h.mu.RLock()
		st, ok := h.topics[t]
		h.mu.RUnlock()
		if !ok {
			continue
		}
		if err := h.sendSnapshot(s, t, st, true); err != nil {
			return err
		}
	}
	return nil
}

func (h *Hub) sendSnapshot(s *Sink, t Topic, st *topicState, resync bool) error {
	raw, err := st.snapshot()
	if err != nil {
		return err
	}
	h.mu.Lock()
	seq := st.seq
	h.mu.Unlock()
	msg := Message{Topic: t, Seq: seq, Snapshot: true, Resync: resync, Data: raw}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	select {
	case s.ch <- msg:
		delete(s.lagged, t)
	default:
		// Snapshot doesn't fit either: stay lagged; the transport retries
		// after draining.
	}
	return nil
}

// ClientOp is the inbound client protocol.
type ClientOp struct {
	Op     string  `json:"op"` // "subscribe" | "unsubscribe"
	Topics []Topic `json:"topics"`
}

// HandleClientOp applies one inbound op to a sink. allow is the
// per-topic authorization the transport derives from the connection's
// principal (audit S11): it answers "may THIS client see THAT topic".
// Unsubscribe is always permitted (dropping a stream is never a
// disclosure); a subscribe the allow function refuses returns one error
// frame per refused topic to the client and subscribes nothing for it.
// A nil allow permits everything (in-process callers; the WS transport
// always supplies one and fails closed on unknown topics).
func (h *Hub) HandleClientOp(_ context.Context, s *Sink, raw []byte, allow func(Topic) bool) error {
	var op ClientOp
	if err := json.Unmarshal(raw, &op); err != nil {
		return err
	}
	switch op.Op {
	case "subscribe":
		permitted := make([]Topic, 0, len(op.Topics))
		var refused []Topic
		for _, t := range op.Topics {
			if allow == nil || allow(t) {
				permitted = append(permitted, t)
			} else {
				refused = append(refused, t)
			}
		}
		if len(permitted) > 0 {
			if err := h.Subscribe(s, permitted...); err != nil {
				return err
			}
		}
		for _, t := range refused {
			h.sendError(s, t, "forbidden")
		}
	case "unsubscribe":
		h.Unsubscribe(s, op.Topics...)
	}
	return nil
}

// sendError enqueues an authorization error frame for one topic. It
// never blocks: a full queue drops the frame — the topic was not
// subscribed, so no data follows that could be misread anyway.
func (h *Hub) sendError(s *Sink, t Topic, reason string) {
	msg := Message{Topic: t, Error: reason}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- msg:
	default:
	}
}

// Clients reports the attached sink count (websocket_clients metric).
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sinks)
}
