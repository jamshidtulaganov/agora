"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Gauge, Timer, ShieldCheck, ShieldAlert, Code2, Zap } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";

// QA Metrics — reads regression as a first-class signal: how much the suite
// runs, how green it stays, how fast each QA agent is, and how far the
// compiled-script model (deterministic ~seconds) has displaced LLM-driven runs.
// Everything comes from GET /api/qa/metrics (test_run + test_case + task
// durations), so it is workspace-wide, not per-project-filtered.

function fmtSec(s: number): string {
  if (!s || s <= 0) return "—";
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const r = s % 60;
  return r ? `${m}m ${r}s` : `${m}m`;
}

function StatCard({
  icon: Icon,
  label,
  value,
  sub,
  tone = "default",
}: {
  icon: typeof Gauge;
  label: string;
  value: string;
  sub?: string;
  tone?: "default" | "good" | "bad";
}) {
  const toneCls =
    tone === "good" ? "text-emerald-500" : tone === "bad" ? "text-destructive" : "text-foreground";
  return (
    <div className="flex flex-col gap-1 rounded-xl border bg-card p-4">
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
        <Icon className="size-3.5" aria-hidden />
        {label}
      </div>
      <div className={`text-2xl font-semibold tabular-nums ${toneCls}`}>{value}</div>
      {sub ? <div className="text-[11px] text-muted-foreground">{sub}</div> : null}
    </div>
  );
}

export function QAMetricsView({ projectId }: { projectId?: string }) {
  const wsId = useWorkspaceId();
  const { data, isLoading } = useQuery({
    // wsId in the key — same cross-workspace staleness fix as the sprint tab.
    queryKey: ["qa-metrics", wsId, projectId ?? "all"],
    queryFn: () => api.getQAMetrics(projectId),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });

  const totals = data?.totals ?? { total: 0, passed: 0, failed: 0, skipped: 0 };
  const coverage = data?.coverage ?? { automated: 0, scripted: 0 };
  const agents = data?.agents ?? [];
  const byDay = data?.by_day ?? [];
  const recent = data?.recent_runs ?? [];

  const passRate = totals.total > 0 ? Math.round((totals.passed / totals.total) * 100) : 0;
  const scriptPct =
    coverage.automated > 0 ? Math.round((coverage.scripted / coverage.automated) * 100) : 0;
  const maxDay = useMemo(() => Math.max(1, ...byDay.map((d) => d.total)), [byDay]);

  if (isLoading && !data) {
    return (
      <div className="px-8 py-6 text-sm text-muted-foreground">Loading QA metrics…</div>
    );
  }

  return (
    <div className="flex w-full flex-col gap-6 px-8 py-6">
      <p className="max-w-2xl text-sm text-muted-foreground">
        Regression health across the workspace (last 30 days). Compiled scripts run
        deterministically in seconds; the rest are LLM-driven — the script coverage below
        is how much of QA has crossed to the fast path.
      </p>

      {/* Top stats */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard icon={Gauge} label="Case runs" value={String(totals.total)} sub="per-case verdicts, last 30 days" />
        <StatCard
          icon={ShieldCheck}
          label="Pass rate"
          value={`${passRate}%`}
          sub={`${totals.passed} passed`}
          tone={passRate >= 80 ? "good" : "default"}
        />
        <StatCard
          icon={ShieldAlert}
          label="Failing"
          value={String(totals.failed)}
          sub={totals.skipped ? `${totals.skipped} skipped/blocked` : "regressions caught"}
          tone={totals.failed > 0 ? "bad" : "good"}
        />
        <StatCard
          icon={Code2}
          label="Script coverage"
          value={`${scriptPct}%`}
          sub={`${coverage.scripted} of ${coverage.automated} automated`}
          tone={scriptPct >= 50 ? "good" : "default"}
        />
        <StatCard
          icon={Zap}
          label="Scripted cases"
          value={String(coverage.scripted)}
          sub="run deterministically (fast path)"
          tone={coverage.scripted > 0 ? "good" : "default"}
        />
      </div>

      {/* Daily trend */}
      <div className="rounded-xl border bg-card p-4">
        <div className="mb-3 text-[12px] font-medium">Regression volume — last 14 days</div>
        {byDay.length === 0 ? (
          <div className="text-[12px] text-muted-foreground">No runs recorded yet.</div>
        ) : (
          <div className="flex items-end gap-1.5" style={{ height: 96 }}>
            {byDay.map((d) => {
              const h = Math.round((d.total / maxDay) * 84) + 4;
              const failH = d.total > 0 ? Math.round((d.failed / d.total) * h) : 0;
              return (
                <div key={d.day} className="flex flex-1 flex-col items-center gap-1" title={`${d.day}: ${d.total} runs, ${d.failed} failed`}>
                  <div className="relative w-full overflow-hidden rounded-sm bg-muted" style={{ height: h }}>
                    <div className="absolute inset-x-0 top-0 bg-emerald-500/70" style={{ height: h - failH }} />
                    <div className="absolute inset-x-0 bottom-0 bg-destructive" style={{ height: failH }} />
                  </div>
                  <span className="text-[9px] text-muted-foreground">{d.day.slice(5)}</span>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Per-agent QA speed */}
      <div className="rounded-xl border bg-card">
        <div className="flex items-center gap-1.5 border-b px-4 py-2.5 text-[12px] font-medium">
          <Timer className="size-3.5 text-muted-foreground" aria-hidden /> QA agent speed
          <span className="ml-1 text-[11px] font-normal text-muted-foreground">
            wall-clock per completed QA task
          </span>
        </div>
        {agents.length === 0 ? (
          <div className="px-4 py-4 text-[12px] text-muted-foreground">No completed QA agent runs yet.</div>
        ) : (
          <table className="w-full text-[12px]">
            <thead>
              <tr className="border-b text-left text-[11px] uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2 font-medium">Agent</th>
                <th className="px-4 py-2 text-right font-medium">Runs</th>
                <th className="px-4 py-2 text-right font-medium">Avg</th>
                <th className="px-4 py-2 text-right font-medium">Min</th>
                <th className="px-4 py-2 text-right font-medium">Max</th>
              </tr>
            </thead>
            <tbody>
              {agents.map((a) => (
                <tr key={a.agent} className="border-b last:border-0">
                  <td className="px-4 py-2 font-medium text-foreground">{a.agent}</td>
                  <td className="px-4 py-2 text-right tabular-nums">{a.runs}</td>
                  <td className="px-4 py-2 text-right tabular-nums">{fmtSec(a.avg_sec)}</td>
                  <td className="px-4 py-2 text-right tabular-nums text-muted-foreground">{fmtSec(a.min_sec)}</td>
                  <td className="px-4 py-2 text-right tabular-nums text-muted-foreground">{fmtSec(a.max_sec)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Recent runs */}
      <div className="rounded-xl border bg-card">
        <div className="border-b px-4 py-2.5 text-[12px] font-medium">Recent regression runs</div>
        {recent.length === 0 ? (
          <div className="px-4 py-4 text-[12px] text-muted-foreground">No runs yet.</div>
        ) : (
          <ul className="divide-y">
            {recent.map((r) => (
              <li key={r.id} className="flex items-center gap-3 px-4 py-2 text-[12px]">
                <span
                  className={
                    r.status === "pass"
                      ? "inline-block size-2 shrink-0 rounded-full bg-emerald-500"
                      : r.status === "fail"
                        ? "inline-block size-2 shrink-0 rounded-full bg-destructive"
                        : "inline-block size-2 shrink-0 rounded-full bg-muted-foreground"
                  }
                  aria-hidden
                />
                <span className="w-16 shrink-0 uppercase text-[10px] tracking-wide text-muted-foreground">
                  {r.status}
                </span>
                {r.issue_number != null ? (
                  <span className="shrink-0 text-muted-foreground">#{r.issue_number}</span>
                ) : null}
                <span className="truncate">{r.case_title || "(untitled case)"}</span>
                <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{r.run_source}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
