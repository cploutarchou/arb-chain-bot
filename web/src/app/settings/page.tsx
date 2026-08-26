"use client";

import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Stat } from "@/components/ui";

export default function SettingsPage() {
  const { state: auth, logout } = useAuth();
  const status = usePoll(() => api.system.status(), 10000);

  return (
    <ConsoleShell active="Settings">
      <PageTitle>Settings, Users &amp; Security</PageTitle>
      <Section title="Session">
        {auth.kind === "authenticated" ? (
          <div className="flex max-w-xl items-center gap-3">
            <Stat label="User" value={auth.me.user_id} />
            <Stat label="Role" value={<Badge tone="ok">{auth.me.role}</Badge>} />
            <Button onClick={() => void logout()} danger>
              Sign out
            </Button>
          </div>
        ) : (
          <p className="text-sm text-[var(--text-dim)]">Not signed in.</p>
        )}
      </Section>
      <Section title="Process">
        <Await state={status} what="system status">
          {(s) => (
            <div className="grid max-w-3xl grid-cols-2 gap-3 md:grid-cols-4">
              <Stat label="Mode" value={s.mode} />
              <Stat label="Version" value={s.version} />
              <Stat label="Uptime" value={`${s.uptime_sec}s`} />
              <Stat label="Components" value={s.components.join(", ")} />
            </div>
          )}
        </Await>
      </Section>
      <Section title="Security posture">
        <ul className="max-w-2xl list-inside list-disc space-y-1 text-[13px] text-[var(--text-dim)]">
          <li>Live trading is permanently disabled by design (LiveExecutor returns ErrLiveTradingDisabled).</li>
          <li>Sessions are server-side and revocable; CSRF required on every state change; RBAC enforced in the backend.</li>
          <li>Exchange access is public market data only — no API keys with trade, withdrawal, or transfer permissions exist anywhere in this system.</li>
          <li>User management (create/disable users, role changes) is not built yet — the bootstrap admin is configured via environment; this page will grow the ADMIN user CRUD when that lands.</li>
          <li>MFA (TOTP) enrollment is reserved in the auth flow but not yet implemented (MASTER_PLAN T-052).</li>
        </ul>
      </Section>
    </ConsoleShell>
  );
}
