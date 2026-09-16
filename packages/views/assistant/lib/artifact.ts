// Pure helpers for the assistant's artifact surfaces (card + pane).
//
// Two different untrusted payloads are decoded here, and neither may throw:
//
//  1. `tool_result` of create_artifact / update_artifact — arbitrary
//     tool-specific JSON (same trust level as tool-summary.ts handles).
//  2. `artifact.content` — a TEXT column the model wrote. For chart/table
//     kinds it is supposed to be a spec matching
//     docs/agora-assistant-artifacts-plan.md §3, but an older server, a newer
//     spec version, or a model that drifted will all land here.
//
// Every function returns `null` on anything it doesn't recognise; the callers
// turn that into the plain tool chip / the raw-content downgrade card, per the
// enum-drift rule in CLAUDE.md. The API *envelope* (id/kind/version/…) is
// validated separately by zod in packages/core/api/schemas.ts — this file only
// deals with the payloads zod deliberately keeps as opaque strings.

import type { AssistantArtifactKind } from "@agora/core/types";

/** Tools whose transcript row renders as an ArtifactCard instead of a chip. */
const ARTIFACT_TOOL_NAMES = new Set(["create_artifact", "update_artifact"]);

export function isArtifactToolName(name: string | null | undefined): boolean {
  return !!name && ARTIFACT_TOOL_NAMES.has(name);
}

const ARTIFACT_KINDS: readonly AssistantArtifactKind[] = [
  "chart",
  "table",
  "markdown",
  "html",
];

/** Narrows a wire `kind` to a kind this build can render, else null. */
export function toArtifactKind(kind: string | undefined | null): AssistantArtifactKind | null {
  return ARTIFACT_KINDS.includes(kind as AssistantArtifactKind)
    ? (kind as AssistantArtifactKind)
    : null;
}

/**
 * What the transcript needs to draw an artifact card without a second fetch.
 * Mirrors the `{artifact_id, title, kind, version}` tool_result contract.
 */
export interface ArtifactToolRef {
  artifactId: string;
  title: string;
  /** May be an unknown future kind — the card falls back to a generic icon. */
  kind: string;
  version: number;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

/**
 * Decode a create_artifact / update_artifact tool result. Returns null when
 * the shape isn't recognisable (older server, error result, tool renamed) so
 * the row degrades to the generic tool chip rather than an empty card.
 */
export function parseArtifactToolResult(result: unknown): ArtifactToolRef | null {
  const obj = asRecord(result);
  if (!obj) return null;

  const artifactId = obj.artifact_id;
  if (typeof artifactId !== "string" || !artifactId.trim()) return null;

  const version = typeof obj.version === "number" && obj.version >= 1 ? obj.version : 1;

  return {
    artifactId,
    title: typeof obj.title === "string" ? obj.title : "",
    kind: typeof obj.kind === "string" ? obj.kind : "",
    version: Math.floor(version),
  };
}

// --- chart spec ---------------------------------------------------------

export type ChartSpecType = "bar" | "line" | "area" | "pie";

const CHART_TYPES: readonly ChartSpecType[] = ["bar", "line", "area", "pie"];

export interface ChartSpecSeries {
  key: string;
  label?: string;
}

export interface ChartSpec {
  type: ChartSpecType;
  /** Row field plotted on the category axis (or used as the pie slice name). */
  x: string;
  series: ChartSpecSeries[];
  rows: Record<string, unknown>[];
}

/**
 * Decode `{type, x, series, rows}`. A spec missing any of those, or carrying
 * an unknown chart type, returns null — the pane then shows the raw content
 * downgrade card instead of an empty chart frame.
 */
export function parseChartSpec(content: string): ChartSpec | null {
  const obj = safeJsonObject(content);
  if (!obj) return null;

  const type = obj.type;
  if (typeof type !== "string" || !CHART_TYPES.includes(type as ChartSpecType)) return null;

  const x = obj.x;
  if (typeof x !== "string" || !x) return null;

  if (!Array.isArray(obj.series) || obj.series.length === 0) return null;
  const series: ChartSpecSeries[] = [];
  for (const entry of obj.series) {
    const item = asRecord(entry);
    const key = item?.key;
    if (typeof key !== "string" || !key) return null;
    series.push({
      key,
      label: typeof item?.label === "string" && item.label ? item.label : undefined,
    });
  }

  if (!Array.isArray(obj.rows)) return null;
  const rows = obj.rows.map(asRecord).filter((row): row is Record<string, unknown> => !!row);

  return { type: type as ChartSpecType, x, series, rows };
}

// --- table spec ---------------------------------------------------------

export type TableCell = string | number | null;

export interface TableSpec {
  columns: string[];
  rows: TableCell[][];
}

/**
 * Decode `{columns, rows}`. Cells of an unexpected type (an object the model
 * forgot to stringify) become null and render as an em dash — losing one cell
 * beats losing the table.
 */
export function parseTableSpec(content: string): TableSpec | null {
  const obj = safeJsonObject(content);
  if (!obj) return null;

  if (!Array.isArray(obj.columns) || obj.columns.length === 0) return null;
  if (!obj.columns.every((column) => typeof column === "string")) return null;

  if (!Array.isArray(obj.rows)) return null;
  const rows: TableCell[][] = [];
  for (const row of obj.rows) {
    if (!Array.isArray(row)) return null;
    rows.push(
      row.map((cell) =>
        typeof cell === "string" || typeof cell === "number" ? cell : null,
      ),
    );
  }

  return { columns: obj.columns as string[], rows };
}

function safeJsonObject(content: string): Record<string, unknown> | null {
  if (!content.trim()) return null;
  try {
    return asRecord(JSON.parse(content));
  } catch {
    return null;
  }
}
