"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Gauge, Timer, ShieldCheck, ShieldAlert, Code2, Zap, BarChart3 } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { useT } from "../../i18n";

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
    <div className="flex flex-col gap-1 rounded-lg border bg-card p-4">
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
        <Icon className="size-3.5" aria-hidden />
        {label}
      </div>
      <div className={`text-2xl font-semibold tabular-nums ${toneCls}`}>{value}</div>
      {sub ? <div className="text-[11px] text-muted-foreground">{sub}</div> : null}
    </div>
  );
}

// Compact dashed-border empty card — mirrors the Suite tab's empty state so
// every cockpit tab reads the same way when there's nothing to show yet.
function EmptyCard({ label }: { label: string }) {
  return (
    <div className="rounded-lg border border-dashed bg-muted/20 px-4 py-6 text-center">
      <BarChart3 className="mx-auto size-5 text-muted-foreground/60" />
      <p className="mt-1.5 text-[12px] text-muted-foreground">{label}</p>
    </div>
  );
}

export function QAMetricsView({ projectId }: { projectId?: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
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
      <div className="w-full space-y-6 px-8 py-8" aria-hidden>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  return (
    <div className="flex w-full flex-col gap-6 px-8 py-8">
      <p className="text-sm text-muted-foreground">{t(($) => $.qa_cockpit.metrics_description)}</p>

      {/* Top stats */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard
          icon={Gauge}
          label={t(($) => $.qa_cockpit.metrics_case_runs)}
          value={String(totals.total)}
          sub={t(($) => $.qa_cockpit.metrics_case_runs_sub)}
        />
        <StatCard
          icon={ShieldCheck}
          label={t(($) => $.qa_cockpit.metrics_pass_rate)}
          value={`${passRate}%`}
          sub={t(($) => $.qa_cockpit.metrics_pass_rate_sub, { count: totals.passed })}
          tone={passRate >= 80 ? "good" : "default"}
        />
        <StatCard
          icon={ShieldAlert}
          label={t(($) => $.qa_cockpit.metrics_failing)}
          value={String(totals.failed)}
          sub={
            totals.skipped
              ? t(($) => $.qa_cockpit.metrics_failing_sub_skipped, { count: totals.skipped })
              : t(($) => $.qa_cockpit.metrics_failing_sub_caught)
          }
          tone={totals.failed > 0 ? "bad" : "good"}
        />
        <StatCard
          icon={Code2}
          label={t(($) => $.qa_cockpit.metrics_script_coverage)}
          value={`${scriptPct}%`}
          sub={t(($) => $.qa_cockpit.metrics_script_coverage_sub, { scripted: coverage.scripted, automated: coverage.automated })}
          tone={scriptPct >= 50 ? "good" : "default"}
        />
        <StatCard
          icon={Zap}
          label={t(($) => $.qa_cockpit.metrics_scripted_cases)}
          value={String(coverage.scripted)}
          sub={t(($) => $.qa_cockpit.metrics_scripted_cases_sub)}
          tone={coverage.scripted > 0 ? "good" : "default"}
        />
      </div>

      {/* Daily trend */}
      <div className="rounded-lg border bg-card p-4">
        <div className="mb-3 text-[12px] font-medium">{t(($) => $.qa_cockpit.metrics_trend_heading)}</div>
        {byDay.length === 0 ? (
          <EmptyCard label={t(($) => $.qa_cockpit.metrics_trend_empty)} />
        ) : (
          <div className="flex items-end gap-1.5" style={{ height: 96 }}>
            {byDay.map((d) => {
              const h = Math.round((d.total / maxDay) * 84) + 4;
              const failH = d.total > 0 ? Math.round((d.failed / d.total) * h) : 0;
              return (
                <div
                  key={d.day}
                  className="flex flex-1 flex-col items-center gap-1"
                  title={t(($) => $.qa_cockpit.metrics_trend_bar_title, { day: d.day, total: d.total, failed: d.failed })}
                >
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
      <div className="rounded-lg border bg-card">
        <div className="flex items-center gap-1.5 border-b px-4 py-2.5 text-[12px] font-medium">
          <Timer className="size-3.5 text-muted-foreground" aria-hidden /> {t(($) => $.qa_cockpit.metrics_agent_speed)}
          <span className="ml-1 text-[11px] font-normal text-muted-foreground">
            {t(($) => $.qa_cockpit.metrics_agent_speed_sub)}
          </span>
        </div>
        {agents.length === 0 ? (
          <div className="p-4">
            <EmptyCard label={t(($) => $.qa_cockpit.metrics_agent_speed_empty)} />
          </div>
        ) : (
          <table className="w-full text-[12px]">
            <thead>
              <tr className="border-b text-left text-[11px] uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2 font-medium">{t(($) => $.qa_cockpit.metrics_table_agent)}</th>
                <th className="px-4 py-2 text-right font-medium">{t(($) => $.qa_cockpit.metrics_table_runs)}</th>
                <th className="px-4 py-2 text-right font-medium">{t(($) => $.qa_cockpit.metrics_table_avg)}</th>
                <th className="px-4 py-2 text-right font-medium">{t(($) => $.qa_cockpit.metrics_table_min)}</th>
                <th className="px-4 py-2 text-right font-medium">{t(($) => $.qa_cockpit.metrics_table_max)}</th>
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
      <div className="rounded-lg border bg-card">
        <div className="border-b px-4 py-2.5 text-[12px] font-medium">{t(($) => $.qa_cockpit.metrics_recent_heading)}</div>
        {recent.length === 0 ? (
          <div className="p-4">
            <EmptyCard label={t(($) => $.qa_cockpit.metrics_recent_empty)} />
          </div>
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
                <span className="truncate">{r.case_title || t(($) => $.qa_cockpit.metrics_untitled_case)}</span>
                <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{r.run_source}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
