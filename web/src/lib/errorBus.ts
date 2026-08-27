// errorBus: a tiny pub/sub so api/client.ts's request() can surface two
// cross-cutting API error codes without every call site handling them
// individually:
//
//   - risk_ack_required (403): the organisation must acknowledge the
//     current risk disclosure before using the platform (billing.md
//     §1.4). auth.tsx subscribes and re-fetches /me so the blocking
//     RiskAckGate renders even if it raced /me on load.
//   - entitlement_exceeded (403): a package limit was hit (packages.md
//     §3.2). ToastProvider subscribes and shows the key/limit with an
//     Upgrade link, regardless of which page/form triggered it.
//
// Both are display-only conveniences; the backend remains the only real
// gate (console-ux-audit.md §6 / design-system.md §2.4).

export interface EntitlementExceededDetail {
  key?: string;
  limit?: unknown;
  message: string;
}

type RiskAckHandler = (requiredVersion?: string) => void;
type EntitlementHandler = (detail: EntitlementExceededDetail) => void;

const riskAckHandlers = new Set<RiskAckHandler>();
const entitlementHandlers = new Set<EntitlementHandler>();

export function onRiskAckRequired(handler: RiskAckHandler): () => void {
  riskAckHandlers.add(handler);
  return () => riskAckHandlers.delete(handler);
}

export function emitRiskAckRequired(requiredVersion?: string) {
  riskAckHandlers.forEach((h) => h(requiredVersion));
}

export function onEntitlementExceeded(handler: EntitlementHandler): () => void {
  entitlementHandlers.add(handler);
  return () => entitlementHandlers.delete(handler);
}

export function emitEntitlementExceeded(detail: EntitlementExceededDetail) {
  entitlementHandlers.forEach((h) => h(detail));
}
