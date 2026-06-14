"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, Copy, KeyRound, Terminal } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core/hooks";
import { runtimeListOptions, runtimeKeys } from "@agora/core/runtimes/queries";
import { deriveRuntimeHealth, type RuntimeHealth } from "@agora/core/runtimes";
import { useWSEvent } from "@agora/core/realtime";
import type { AgentRuntime } from "@agora/core/types";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { copyText } from "@agora/ui/lib/clipboard";
import { cn } from "@agora/ui/lib/utils";
import { PageHeader } from "../../layout/page-header";
import { ProviderLogo } from "./provider-logo";
import { useT } from "../../i18n";

// The four agent CLIs surfaced as "AI accounts". Kept as a const tuple so the
// page renders a stable card per provider even when no runtime exists yet —
// "not connected" is a first-class state, not an empty list.
const ACCOUNT_PROVIDERS = ["claude", "codex", "gemini", "antigravity"] as const;
type AccountProvider = (typeof ACCOUNT_PROVIDERS)[number];

// The auth states the daemon writes into runtime metadata.auth_state. Mirrors
// the Go AuthState constants in server/internal/daemon/authprobe.go.
type AuthState = "logged_in" | "logged_out" | "unknown";

// Per-provider connect summary derived from the runtime list. This is the
// pure, testable core of the page — no React, no i18n.
export interface ProviderAccount {
  provider: AccountProvider;
  /** True when at least one runtime for this provider is currently online. */
  online: boolean;
  /** Best derived health across this provider's runtimes (online wins). */
  health: RuntimeHealth | "none";
  /** auth_state read from the chosen runtime's metadata, defaulting to unknown. */
  authState: AuthState;
  /** account_email from metadata, when present. */
  email: string | null;
  /** account_plan from metadata, when present. */
  plan: string | null;
  /** How many runtimes of this provider the developer has. */
  runtimeCount: number;
}

// Health ranking used to pick the "best" runtime to represent a provider:
// a live, signed-in runtime should win over an offline one.
const HEALTH_RANK: Record<RuntimeHealth, number> = {
  online: 3,
  recently_lost: 2,
  offline: 1,
  about_to_gc: 0,
};

// Defensive reader: runtime.metadata is Record<string, unknown> and older
// runtimes (registered before this feature shipped) won't carry the auth keys
// at all. Every access goes through these so a missing/malformed value
// degrades to a safe default instead of throwing.
function readAuthState(metadata: Record<string, unknown>): AuthState {
  const raw = metadata?.["auth_state"];
  if (raw === "logged_in" || raw === "logged_out" || raw === "unknown") {
    return raw;
  }
  return "unknown";
}

function readString(metadata: Record<string, unknown>, key: string): string | null {
  const raw = metadata?.[key];
  if (typeof raw === "string" && raw.trim() !== "") return raw.trim();
  return null;
}

/**
 * Group the runtime list into one ProviderAccount per known provider.
 *
 * For each provider we pick a single representative runtime — preferring the
 * healthiest, then a logged_in one — and read its auth metadata. Providers
 * with no runtime at all still get a card (health "none", authState unknown)
 * so the page always shows the full provider matrix.
 *
 * Pure (no hooks) so it can be unit-tested directly.
 */
export function deriveProviderAccounts(
  runtimes: AgentRuntime[],
  now: number,
): ProviderAccount[] {
  return ACCOUNT_PROVIDERS.map((provider) => {
    const mine = runtimes.filter((r) => r.provider === provider);
    if (mine.length === 0) {
      return {
        provider,
        online: false,
        health: "none" as const,
        authState: "unknown" as AuthState,
        email: null,
        plan: null,
        runtimeCount: 0,
      };
    }

    const online = mine.some((r) => r.status === "online");

    // Choose the representative runtime: best health first; ties broken by
    // preferring a logged_in metadata state so the card shows a real account
    // when any runtime has one.
    const ranked = [...mine].sort((a, b) => {
      const ha = HEALTH_RANK[deriveRuntimeHealth(a, now)];
      const hb = HEALTH_RANK[deriveRuntimeHealth(b, now)];
      if (hb !== ha) return hb - ha;
      const la = readAuthState(a.metadata) === "logged_in" ? 1 : 0;
      const lb = readAuthState(b.metadata) === "logged_in" ? 1 : 0;
      return lb - la;
    });
    const chosen = ranked[0]!;

    return {
      provider,
      online,
      health: deriveRuntimeHealth(chosen, now),
      authState: readAuthState(chosen.metadata),
      email: readString(chosen.metadata, "account_email"),
      plan: readString(chosen.metadata, "account_plan"),
      runtimeCount: mine.length,
    };
  });
}

// Re-render every 30s so derived health (recently_lost → offline) catches up
// even when no query data changed — mirrors RuntimesPage.useNowTick.
function useNowTick(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

export function AiAccountsPage() {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const now = useNowTick();

  // Scope to the current developer's own runtimes ("me") — this page is each
  // developer's view of THEIR agent-CLI accounts, not the whole workspace.
  const { data: runtimes = [], isLoading } = useQuery(
    runtimeListOptions(wsId, "me"),
  );

  // Live-refresh when a daemon (re)registers, so signing a CLI in and
  // restarting the daemon reflects here without a manual reload.
  useWSEvent("daemon:register", () => {
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
  });

  const accounts = useMemo(
    () => deriveProviderAccounts(runtimes, now),
    [runtimes, now],
  );

  const anyConnected = accounts.some((a) => a.authState === "logged_in");

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="px-5">
        <div className="flex items-center gap-2">
          <KeyRound className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">{t(($) => $.ai_accounts.title)}</h1>
        </div>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-auto border-t bg-background">
        <div className="mx-auto w-full max-w-3xl px-5 py-6">
          <p className="mb-6 max-w-2xl text-sm text-muted-foreground">
            {t(($) => $.ai_accounts.subtitle)}
          </p>

          {isLoading ? (
            <div className="space-y-3" data-testid="ai-accounts-loading">
              {ACCOUNT_PROVIDERS.map((p) => (
                <Skeleton key={p} className="h-24 w-full rounded-lg" />
              ))}
            </div>
          ) : (
            <div className="space-y-3">
              {accounts.map((account) => (
                <ProviderAccountCard key={account.provider} account={account} />
              ))}
            </div>
          )}

          {!isLoading && !anyConnected && (
            <div className="mt-6 rounded-lg border border-dashed p-4 text-center">
              <p className="text-sm font-medium">
                {t(($) => $.ai_accounts.empty.title)}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {t(($) => $.ai_accounts.empty.hint)}
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// Maps the derived per-provider summary to the visible status. "offline" is
// distinct from "not connected": we know whether a runtime is reachable
// separately from whether the CLI has an account session.
type DisplayState = "connected" | "not_connected" | "unknown" | "offline";

function displayStateFor(account: ProviderAccount): DisplayState {
  // No runtime online for this provider at all → offline guidance wins, since
  // we can't trust a stale auth_state from a runtime that's gone.
  if (!account.online) return "offline";
  if (account.authState === "logged_in") return "connected";
  if (account.authState === "logged_out") return "not_connected";
  return "unknown";
}

const STATE_DOT: Record<DisplayState, string> = {
  connected: "bg-success",
  not_connected: "bg-warning",
  unknown: "bg-muted-foreground/40",
  offline: "bg-muted-foreground/40",
};

function ProviderAccountCard({ account }: { account: ProviderAccount }) {
  const { t } = useT("runtimes");
  const state = displayStateFor(account);
  const connected = state === "connected";

  const providerLabel = t(($) => $.ai_accounts.providers[account.provider]);
  const stateLabel = t(($) => $.ai_accounts.state[state]);
  const statusHint = t(($) => $.ai_accounts.status_hint[state]);

  return (
    <div
      className="rounded-lg border bg-card p-4"
      data-testid={`ai-account-${account.provider}`}
      data-state={state}
    >
      <div className="flex items-start gap-3">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md border bg-background">
          <ProviderLogo provider={account.provider} className="h-5 w-5" />
        </span>

        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h2 className="text-sm font-semibold">{providerLabel}</h2>
            <span className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs text-muted-foreground">
              <span
                className={cn("h-1.5 w-1.5 rounded-full", STATE_DOT[state])}
              />
              {stateLabel}
            </span>
            {account.runtimeCount > 0 && (
              <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
                {t(($) => $.ai_accounts.account.runtimes_count, {
                  count: account.runtimeCount,
                })}
              </span>
            )}
          </div>

          {connected ? (
            <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">
                {t(($) => $.ai_accounts.account.email_label)}:{" "}
                <span className="font-medium text-foreground">
                  {account.email ??
                    t(($) => $.ai_accounts.account.email_unknown)}
                </span>
              </span>
              <span className="text-muted-foreground">
                {t(($) => $.ai_accounts.account.plan_label)}:{" "}
                <span className="font-medium text-foreground">
                  {account.plan ??
                    t(($) => $.ai_accounts.account.plan_unknown)}
                </span>
              </span>
            </div>
          ) : (
            <p className="mt-2 text-xs text-muted-foreground">{statusHint}</p>
          )}

          {!connected && <ConnectHint provider={account.provider} />}
        </div>
      </div>
    </div>
  );
}

// Read-only guidance: the command the developer runs on the machine to sign
// this CLI into its account. No secret entry happens in the UI — we only show
// what to run, with a copy affordance.
function ConnectHint({ provider }: { provider: AccountProvider }) {
  const { t } = useT("runtimes");
  const command = t(($) => $.ai_accounts.connect[provider]);
  const copyAria = t(($) => $.ai_accounts.connect.copy_aria);

  return (
    <div className="mt-3">
      <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        {t(($) => $.ai_accounts.connect.title)}
      </p>
      <div className="flex items-start gap-2 rounded-md bg-muted px-3 py-2 font-mono text-xs">
        <Terminal
          className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground"
          aria-hidden
        />
        <code className="min-w-0 flex-1 break-all whitespace-pre-wrap">
          {command}
        </code>
        <CopyButton text={command} ariaLabel={copyAria} />
      </div>
    </div>
  );
}

function CopyButton({ text, ariaLabel }: { text: string; ariaLabel: string }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const id = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(id);
  }, [copied]);

  return (
    <button
      type="button"
      onClick={() => {
        void copyText(text).then((ok) => {
          if (ok) setCopied(true);
        });
      }}
      aria-label={ariaLabel}
      className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-success" aria-hidden />
      ) : (
        <Copy className="h-3.5 w-3.5" aria-hidden />
      )}
    </button>
  );
}

export default AiAccountsPage;
