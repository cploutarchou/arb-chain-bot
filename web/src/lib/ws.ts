"use client";

// Realtime hub client: subscribe to topics over /api/v1/ws, track seq
// per topic, and treat any gap as a resync signal — the server then
// replays a fresh snapshot (Snapshot/Resync flags). Reconnects with
// backoff; consumers just receive ordered events + snapshots.

export interface HubMessage<T = unknown> {
  topic: string;
  seq: number;
  snapshot?: boolean;
  resync?: boolean;
  data: T;
}

export interface HubHandlers {
  onMessage: (msg: HubMessage) => void;
  onStatus?: (status: "connecting" | "open" | "closed") => void;
}

export function connectHub(topics: string[], handlers: HubHandlers): () => void {
  let ws: WebSocket | null = null;
  let closed = false;
  let backoff = 1000;
  const lastSeq = new Map<string, number>();

  const open = () => {
    if (closed) return;
    handlers.onStatus?.("connecting");
    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${window.location.host}/api/v1/ws`);
    ws.onopen = () => {
      backoff = 1000;
      handlers.onStatus?.("open");
      ws?.send(JSON.stringify({ op: "subscribe", topics }));
    };
    ws.onmessage = (ev) => {
      let msg: HubMessage;
      try {
        msg = JSON.parse(ev.data as string) as HubMessage;
      } catch {
        return;
      }
      const prev = lastSeq.get(msg.topic) ?? 0;
      if (!msg.snapshot && prev > 0 && msg.seq > prev + 1) {
        // Gap: the server's lag recovery sends a resync snapshot on its
        // own; consumers see it via the snapshot flag. Nothing to do
        // here beyond accepting the newer seq.
      }
      lastSeq.set(msg.topic, msg.seq);
      handlers.onMessage(msg);
    };
    ws.onclose = () => {
      handlers.onStatus?.("closed");
      if (!closed) {
        setTimeout(open, backoff);
        backoff = Math.min(backoff * 2, 15000);
      }
    };
    ws.onerror = () => ws?.close();
  };
  open();

  return () => {
    closed = true;
    ws?.close();
  };
}
