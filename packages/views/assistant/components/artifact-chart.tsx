"use client";

import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from "recharts";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@agora/ui/components/ui/chart";
import type { ChartSpec } from "../lib/artifact";

/**
 * A `chart` artifact rendered natively through the app's own Recharts stack
 * (the Usage-page stack) rather than as sandboxed HTML: the model ships data
 * only, so the result is theme-correct in light and dark, and there is no
 * chart library to smuggle inside an artifact document.
 *
 * Colors come from the chart CSS vars, cycled by series index — the same five
 * tokens every other chart in the product uses.
 */
const SERIES_COLOR_VARS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
] as const;

function seriesColor(index: number): string {
  return SERIES_COLOR_VARS[index % SERIES_COLOR_VARS.length]!;
}

export function ArtifactChart({ spec }: { spec: ChartSpec }) {
  const config: ChartConfig = Object.fromEntries(
    spec.series.map((series, index) => [
      series.key,
      { label: series.label ?? series.key, color: seriesColor(index) },
    ]),
  );

  return (
    <ChartContainer config={config} className="aspect-[16/10] w-full">
      {renderChart(spec)}
    </ChartContainer>
  );
}

function renderChart(spec: ChartSpec) {
  const margin = { left: 4, right: 8, top: 8, bottom: 0 };

  // A pie plots exactly one measure — the first series — with the category
  // axis field naming the slices.
  if (spec.type === "pie") {
    const key = spec.series[0]!.key;
    return (
      <PieChart margin={margin}>
        <ChartTooltip content={<ChartTooltipContent nameKey={spec.x} />} />
        <Pie data={spec.rows} dataKey={key} nameKey={spec.x} innerRadius="45%" strokeWidth={2}>
          {spec.rows.map((_row, index) => (
            <Cell key={index} fill={seriesColor(index)} />
          ))}
        </Pie>
        <ChartLegend content={<ChartLegendContent nameKey={spec.x} />} />
      </PieChart>
    );
  }

  const axes = (
    <>
      <CartesianGrid vertical={false} />
      <XAxis
        dataKey={spec.x}
        tickLine={false}
        axisLine={false}
        tickMargin={8}
        interval="preserveStartEnd"
      />
      <YAxis tickLine={false} axisLine={false} tickMargin={8} width={44} />
      <ChartTooltip content={<ChartTooltipContent />} />
      {spec.series.length > 1 && <ChartLegend content={<ChartLegendContent />} />}
    </>
  );

  if (spec.type === "line") {
    return (
      <LineChart data={spec.rows} margin={margin}>
        {axes}
        {spec.series.map((series) => (
          <Line
            key={series.key}
            type="monotone"
            dataKey={series.key}
            stroke={`var(--color-${series.key})`}
            strokeWidth={2}
            dot={false}
          />
        ))}
      </LineChart>
    );
  }

  if (spec.type === "area") {
    return (
      <AreaChart data={spec.rows} margin={margin}>
        {axes}
        {spec.series.map((series) => (
          <Area
            key={series.key}
            type="monotone"
            dataKey={series.key}
            stroke={`var(--color-${series.key})`}
            fill={`var(--color-${series.key})`}
            fillOpacity={0.2}
            strokeWidth={2}
            stackId={spec.series.length > 1 ? "artifact" : undefined}
          />
        ))}
      </AreaChart>
    );
  }

  return (
    <BarChart data={spec.rows} margin={margin}>
      {axes}
      {spec.series.map((series) => (
        <Bar
          key={series.key}
          dataKey={series.key}
          fill={`var(--color-${series.key})`}
          stackId={spec.series.length > 1 ? "artifact" : undefined}
          radius={[3, 3, 0, 0]}
        />
      ))}
    </BarChart>
  );
}
