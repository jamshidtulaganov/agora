"use client";

import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, AlertTriangle, RefreshCcw, Repeat, Timer } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import type { PolicyFleetHealth } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../../navigation";

// Policy Agent — the fleet cockpit. The supervisor's read on agent SPEED + health
// across the workspace: per-agent run duration / queue wait, and the flagged
// tasks the team should improve — stalled (stuck running = likely a context-error
// loop), failed (with the classifier), and looping issues (one issue churning
// many tasks). Read-only; auto-cancel of a stalled/looping task is a follow-up.

function secs(n: number): string {
  if (!n || n < 0) return "—";
  if (n < 90) return `${n.toFixed(n < 10 ? 1 : 0)}s`;
  const m = Math.floor(n / 60);
  const s = Math.round(n % 60);
  return `${m}m ${s}s`;
}

export function PolicyPage() {
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const { data, isLoading } = useQuery({
    queryKey: ["policy-fleet-health", wsId],
    queryFn: () => api.getPolicyFleetHealth(),
    staleTime: 15_000,
    refetchInterval: 30_000,
  });

  const d: PolicyFleetHealth =
    data ?? { stall_minutes: 20, loop_threshold: 4, agents: [], stalled: [], recent_failures: [], looping: [] };

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6 px-6 py-8">
      <header className="space-y-1">
        <div className="flex items-center gap-2">
          <Activity className="size-5" />
          <h1 className="text-lg font-semibold">Policy</h1>
        </div>
        <p className="text-sm text-muted-foreground">
          Agent fleet speed + health (last 7d). {d.agents.length} agents ·{" "}
          <span className={d.stalled.length ? "text-destructive" : ""}>{d.stalled.length} stalled</span> ·{" "}
          {d.recent_failures.length} failed (24h) · {d.looping.length} looping
        </p>
      </header>

      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <div className="space-y-6">
          {/* Per-agent speed */}
          <Section icon={Timer} title="Agent speed (last 7 days)">
            {d.agents.length === 0 ? (
              <Empty />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-[12px]">
                  <thead className="text-left text-muted-foreground">
                    <tr className="border-b">
                      <th className="px-3 py-1.5 font-medium">Agent</th>
                      <th className="px-3 py-1.5 font-medium">Tasks</th>
                      <th className="px-3 py-1.5 font-medium">Failed</th>
                      <th className="px-3 py-1.5 font-medium">Avg run</th>
                      <th className="px-3 py-1.5 font-medium">p95 run</th>
                      <th className="px-3 py-1.5 font-medium">Avg queue</th>
                    </tr>
                  </thead>
                  <tbody>
                    {d.agents.map((a) => (
                      <tr key={a.agent_id} className="border-b last:border-0">
                        <td className="px-3 py-1.5">{a.agent_name}</td>
                        <td className="px-3 py-1.5">{a.task_count}</td>
                        <td className={cn("px-3 py-1.5", a.failed_count > 0 && "text-destructive")}>
                          {a.failed_count}
                        </td>
                        <td className="px-3 py-1.5 font-mono">{secs(a.avg_run_seconds)}</td>
                        <td className="px-3 py-1.5 font-mono">{secs(a.p95_run_seconds)}</td>
                        <td className="px-3 py-1.5 font-mono">{secs(a.avg_queue_seconds)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Section>

          {/* Stalled */}
          <Section
            icon={AlertTriangle}
            iconClass="text-destructive"
            title={`Stalled — running > ${d.stall_minutes}m (likely a context-error loop)`}
          >
            {d.stalled.length === 0 ? (
              <Empty label="No stalled runs." />
            ) : (
              <ul className="divide-y">
                {d.stalled.map((s) => (
                  <li key={s.task_id} className="flex items-center gap-2 px-3 py-1.5 text-[12px]">
                    <span className="font-medium">{s.agent_name}</span>
                    <AppLink href={wp.issueDetail(s.issue_id)} className="text-muted-foreground hover:underline">
                      issue
                    </AppLink>
                    <span className="ml-auto font-mono text-muted-foreground">
                      since {s.started_at ? new Date(s.started_at).toLocaleTimeString() : "?"} · attempt {s.attempt}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Section>

          {/* Recent failures */}
          <Section icon={RefreshCcw} title="Recent failures (24h)">
            {d.recent_failures.length === 0 ? (
              <Empty label="No failures." />
            ) : (
              <ul className="divide-y">
                {d.recent_failures.map((f) => (
                  <li key={f.task_id} className="px-3 py-1.5 text-[12px]">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{f.agent_name}</span>
                      <span className="rounded bg-muted px-1.5 py-0.5 text-[10.5px]">
                        {f.failure_reason || "failed"}
                      </span>
                      <AppLink href={wp.issueDetail(f.issue_id)} className="text-muted-foreground hover:underline">
                        issue
                      </AppLink>
                      <span className="ml-auto text-muted-foreground">attempt {f.attempt}</span>
                    </div>
                    {f.error && (
                      <div className="mt-0.5 truncate font-mono text-[10.5px] text-muted-foreground">{f.error}</div>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </Section>

          {/* Looping */}
          <Section icon={Repeat} title={`Looping issues — ≥ ${d.loop_threshold} tasks in the last hour`}>
            {d.looping.length === 0 ? (
              <Empty label="No looping issues." />
            ) : (
              <ul className="divide-y">
                {d.looping.map((l) => (
                  <li key={l.issue_id} className="flex items-center gap-2 px-3 py-1.5 text-[12px]">
                    <AppLink href={wp.issueDetail(l.issue_id)} className="hover:underline">
                      issue {l.issue_id.slice(0, 8)}
                    </AppLink>
                    <span className="ml-auto font-mono text-destructive">{l.task_count} tasks / h</span>
                  </li>
                ))}
              </ul>
            )}
          </Section>
        </div>
      )}
    </div>
  );
}

function Section({
  icon: Icon,
  iconClass,
  title,
  children,
}: {
  icon: typeof Activity;
  iconClass?: string;
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="rounded-lg border">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Icon className={cn("size-4 shrink-0", iconClass)} />
        <span className="text-sm font-medium">{title}</span>
      </div>
      {children}
    </section>
  );
}

function Empty({ label = "Nothing here." }: { label?: string }) {
  return <p className="px-3 py-2 text-[12px] text-muted-foreground">{label}</p>;
}
