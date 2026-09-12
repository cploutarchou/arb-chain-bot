"use client";

// Telegram (BL-21): status only — never shows a token. Allowlist
// management lives on Settings (the platform-settings document), so
// this page links there rather than duplicating that editor.

import Link from "next/link";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, PageTitle, Section, Stat, fmtTime } from "@/components/ui";

export default function TelegramPage() {
  const status = usePoll(() => api.telegram.status(), 10000);

  return (
    <ConsoleShell>
      <PageTitle>Telegram</PageTitle>
      <Await state={status} what="telegram status">
        {(s) => (
          <>
            <Section title="Bot status">
              {!s.enabled ? (
                <p className="text-sm text-[var(--text-dim)]">
                  Telegram is not configured for this deployment (needs a bot token and at least one
                  allowlisted chat). This never exposes the token from the console — configuring it is a
                  deploy-time step; the chat allowlist itself is editable on{" "}
                  <Link href="/settings#notifications" className="text-[var(--accent)] underline">
                    Settings → Notifications
                  </Link>
                  .
                </p>
              ) : (
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat label="Enabled" value="yes" tone="ok" />
                  <Stat label="Bot username" value={s.bot_username ?? "—"} />
                  <Stat label="Allowlisted chats" value={(s.allowlist ?? []).length} />
                  <Stat label="Messages received" value={s.messages} />
                  <Stat label="Errors" value={s.errors} tone={s.errors > 0 ? "warn" : undefined} />
                  <Stat
                    label="Last poll"
                    value={s.last_poll_at ? fmtTime(s.last_poll_at) : "—"}
                    tone={s.last_poll_ok ? "ok" : "warn"}
                  />
                  {s.last_poll_error && <Stat label="Last poll error" value={s.last_poll_error} tone="warn" />}
                  <Stat
                    label="Last getMe"
                    value={s.last_getme_at ? fmtTime(s.last_getme_at) : "—"}
                    tone={s.last_getme_ok ? "ok" : "warn"}
                  />
                  {s.last_getme_error && <Stat label="Last getMe error" value={s.last_getme_error} tone="warn" />}
                  <Stat label="Pushes sent" value={s.pushes_sent} />
                  <Stat label="Push errors" value={s.push_errors} tone={s.push_errors > 0 ? "warn" : undefined} />
                  <Stat label="Last pushed" value={s.last_pushed_at ? fmtTime(s.last_pushed_at) : "—"} />
                </div>
              )}
            </Section>
            {s.enabled && (
              <Section title="Allowlist">
                <p className="text-sm text-[var(--text-dim)]">
                  Chat ids: {(s.allowlist ?? []).length > 0 ? (s.allowlist ?? []).join(", ") : "none"}. Manage the
                  allowlist on{" "}
                  <Link href="/settings#notifications" className="text-[var(--accent)] underline">
                    Settings → Notifications
                  </Link>
                  .
                </p>
              </Section>
            )}
          </>
        )}
      </Await>
    </ConsoleShell>
  );
}
