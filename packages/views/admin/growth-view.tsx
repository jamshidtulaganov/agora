"use client";

import { useQuery } from "@tanstack/react-query";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Button } from "@agora/ui/components/ui/button";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@agora/ui/components/ui/chart";
import {
  adminGrowthOptions,
  type AdminFunnelRow,
  type AdminGrowth,
} from "@agora/core/platform-admin";
import { useT } from "../i18n";

export const RANGES = [7, 30, 90] as const;

// A URL fragment, not prose: identical in every language.
const UTM_EXAMPLE = "?utm_source=telegram&utm_medium=post&utm_campaign=launch";
export type Range = (typeof RANGES)[number];

export function RangePicker({ value, onChange }: { value: Range; onChange: (r: Range) => void }) {
  const { t } = useT("admin");
  return (
    <div className="inline-flex rounded-md border p-0.5" role="group" aria-label={t(($) => $.growth.range_label)}>
      {RANGES.map((r) => (
        <Button
          key={r}
          size="sm"
          variant={r === value ? "secondary" : "ghost"}
          aria-pressed={r === value}
          className="h-7 px-2.5"
          onClick={() => onChange(r)}
        >
          {t(($) => $.growth.range, { days: r })}
        </Button>
      ))}
    </div>
  );
}

function shortDay(day: string): string {
  const d = new Date(`${day}T00:00:00Z`);
  return Number.isNaN(d.getTime())
    ? day
    : d.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" });
}

type SeriesKey = "signups" | "active_users" | "new_workspaces";

// One measure per chart (small multiples): the three counts have different
// scales, and one shared axis would flatten the small ones.
function DailyBarChart({ data, series, label }: { data: AdminGrowth["days"]; series: SeriesKey; label: string }) {
  const config = { [series]: { label, color: "var(--chart-1)" } } satisfies ChartConfig;
  const rows = data.map((d) => ({ label: shortDay(d.day), [series]: d[series] }));
  return (
    <ChartContainer config={config} className="aspect-[3/1] w-full">
      <BarChart data={rows} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} interval="preserveStartEnd" minTickGap={24} />
        <YAxis tickLine={false} axisLine={false} tickMargin={8} allowDecimals={false} width={32} />
        <ChartTooltip cursor={{ fillOpacity: 0.4 }} content={<ChartTooltipContent />} />
        <Bar dataKey={series} fill={`var(--color-${series})`} radius={[4, 4, 0, 0]} />
      </BarChart>
    </ChartContainer>
  );
}

export function GrowthView({ range }: { range: Range }) {
  const { t } = useT("admin");
  const { data, isLoading, error } = useQuery(adminGrowthOptions(range));
  if (isLoading) return <p className="text-sm text-muted-foreground">{t(($) => $.loading)}</p>;
  if (error || !data) return <p className="text-sm text-muted-foreground">{t(($) => $.error)}</p>;

  const total = (key: SeriesKey) => data.days.reduce((sum, d) => sum + d[key], 0);
  const peakActive = data.days.reduce((max, d) => Math.max(max, d.active_users), 0);
  const charts: { key: SeriesKey; label: string; headline: string }[] = [
    { key: "signups", label: t(($) => $.growth.signups), headline: String(total("signups")) },
    {
      key: "active_users",
      label: t(($) => $.growth.active_users),
      headline: t(($) => $.growth.peak, { count: peakActive }),
    },
    { key: "new_workspaces", label: t(($) => $.growth.new_workspaces), headline: String(total("new_workspaces")) },
  ];

  return (
    <div className="grid gap-3 lg:grid-cols-3">
      {charts.map((c) => (
        <Card key={c.key} size="sm">
          <CardContent className="space-y-3">
            <div className="flex items-baseline justify-between gap-2">
              <p className="truncate text-sm font-medium">{c.label}</p>
              <p className="shrink-0 text-sm tabular-nums text-muted-foreground">{c.headline}</p>
            </div>
            <DailyBarChart data={data.days} series={c.key} label={c.label} />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function pct(part: number, whole: number): string {
  return whole > 0 ? `${Math.round((part / whole) * 100)}%` : "—";
}

function FunnelTable({ title, rows, direct }: { title: string; rows: AdminFunnelRow[]; direct: string }) {
  const { t } = useT("admin");
  const max = rows.reduce((m, r) => Math.max(m, r.signups), 0);
  return (
    <Card size="sm">
      <CardContent className="space-y-3">
        <p className="text-sm font-medium">{title}</p>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t(($) => $.marketing.empty)}</p>
        ) : (
          <table className="w-full table-fixed text-sm">
            <colgroup>
              <col className="w-[46%]" />
              <col />
              <col />
              <col />
            </colgroup>
            <thead>
              <tr className="text-left text-xs text-muted-foreground">
                <th className="pb-2 font-normal">{t(($) => $.marketing.source)}</th>
                <th className="truncate pb-2 pl-2 text-right font-normal">{t(($) => $.marketing.signups)}</th>
                <th className="truncate pb-2 pl-2 text-right font-normal">{t(($) => $.marketing.workspace)}</th>
                <th className="truncate pb-2 pl-2 text-right font-normal">{t(($) => $.marketing.paying)}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key} className="border-t border-border/60">
                  <td className="py-1.5 pr-3">
                    <div className="truncate" title={r.key === "(direct)" ? direct : r.key}>
                      {r.key === "(direct)" ? direct : r.key}
                    </div>
                    {/* Magnitude bar: one hue, length = share of the top row. */}
                    <div className="mt-1 h-1 rounded-full bg-muted">
                      <div
                        className="h-1 rounded-full bg-[var(--chart-1)]"
                        style={{ width: `${max > 0 ? Math.max(4, (r.signups / max) * 100) : 0}%` }}
                      />
                    </div>
                  </td>
                  <td className="py-1.5 pl-2 text-right tabular-nums">{r.signups}</td>
                  <td className="whitespace-nowrap py-1.5 pl-2 text-right tabular-nums text-muted-foreground">
                    {r.owns_workspace} · {pct(r.owns_workspace, r.signups)}
                  </td>
                  <td className="whitespace-nowrap py-1.5 pl-2 text-right tabular-nums text-muted-foreground">
                    {r.paying} · {pct(r.paying, r.signups)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  );
}

export function MarketingView({ range }: { range: Range }) {
  const { t } = useT("admin");
  const { data, isLoading, error } = useQuery(adminGrowthOptions(range));
  if (isLoading) return <p className="text-sm text-muted-foreground">{t(($) => $.loading)}</p>;
  if (error || !data) return <p className="text-sm text-muted-foreground">{t(($) => $.error)}</p>;
  const direct = t(($) => $.marketing.direct);

  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        {t(($) => $.marketing.attributed, { attributed: data.attributed, total: data.signups })}
      </p>
      <div className="grid gap-3 lg:grid-cols-2">
        <FunnelTable title={t(($) => $.marketing.sources)} rows={data.sources} direct={direct} />
        <FunnelTable title={t(($) => $.marketing.campaigns)} rows={data.campaigns} direct={t(($) => $.marketing.no_campaign)} />
        <FunnelTable title={t(($) => $.marketing.referrers)} rows={data.referrers} direct={direct} />
        <FunnelTable title={t(($) => $.marketing.landing_pages)} rows={data.landing_pages} direct={t(($) => $.marketing.unknown_page)} />
      </div>
      <p className="text-xs text-muted-foreground">
        {t(($) => $.marketing.tip)}{" "}
        <code className="rounded bg-muted px-1 py-0.5">{UTM_EXAMPLE}</code>
      </p>
    </div>
  );
}
