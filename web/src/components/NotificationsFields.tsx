"use client";

// Shared Notifications (cooldown + severity->channel routing) form used
// by both the structured Strategy form (/strategies, BL-14) and the
// Settings "Notifications" section (BL-12) — both write the same
// NotificationParams leaf of the one StrategyParams document.

import {
  NOTIFICATION_CHANNELS,
  NOTIFICATION_COOLDOWN_HELP,
  NOTIFICATION_SEVERITIES,
} from "@/lib/strategyFields";

export function NotificationsFields({
  cooldownRaw,
  onCooldownChange,
  cooldownError,
  routes,
  onToggleChannel,
  disabled,
}: {
  cooldownRaw: string;
  onCooldownChange: (v: string) => void;
  cooldownError?: string | null;
  routes: Record<string, string[]>;
  onToggleChannel: (severity: string, channel: string, enabled: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <div className="max-w-xl space-y-4">
      <div>
        <label className="mb-1 block text-[12px] text-[var(--text-dim)]" htmlFor="notif-cooldown">
          Alert cooldown (seconds)
        </label>
        <input
          id="notif-cooldown"
          type="text"
          inputMode="numeric"
          value={cooldownRaw}
          disabled={disabled}
          onChange={(e) => onCooldownChange(e.target.value)}
          className={`w-40 rounded border bg-[var(--bg)] px-2 py-1 text-[13px] outline-none disabled:opacity-50 ${
            cooldownError ? "border-[var(--critical)]" : "border-[var(--border)] focus:border-[var(--accent)]"
          }`}
        />
        <p className="mt-1 text-[11px] text-[var(--text-dim)]">{NOTIFICATION_COOLDOWN_HELP}</p>
        {cooldownError && <p className="mt-1 text-[11px] text-[var(--critical)]">{cooldownError}</p>}
      </div>
      <div>
        <div className="mb-1 text-[12px] text-[var(--text-dim)]">Severity → channel routing</div>
        <table className="border-collapse text-[12px]">
          <thead>
            <tr>
              <th className="pr-4 text-left font-medium text-[var(--text-dim)]">Severity</th>
              {NOTIFICATION_CHANNELS.map((c) => (
                <th key={c} className="px-3 text-left font-medium text-[var(--text-dim)]">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {NOTIFICATION_SEVERITIES.map((sev) => {
              const chans = routes[sev] ?? [];
              return (
                <tr key={sev}>
                  <td className="pr-4 py-1">{sev}</td>
                  {NOTIFICATION_CHANNELS.map((c) => (
                    <td key={c} className="px-3 py-1">
                      <input
                        type="checkbox"
                        aria-label={`${sev} to ${c}`}
                        checked={chans.includes(c)}
                        disabled={disabled}
                        onChange={(e) => onToggleChannel(sev, c, e.target.checked)}
                      />
                    </td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </table>
        <p className="mt-1 text-[11px] text-[var(--text-dim)]">
          A severity with no channel checked falls back to web only.
        </p>
      </div>
    </div>
  );
}
