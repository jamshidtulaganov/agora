// Client-side export of an assistant artifact — no server round trip.
//
// Everything here is pure so the serialisation can be tested without a DOM:
// `artifactExport()` decides the bytes, the extension and the media type, and
// the pane's download button is a five-line Blob/anchor wrapper around it.
//
// Each kind exports in the format a person actually wants to open it in:
// markdown → .md, html → .html, chart spec → .json, table → .csv. A table is
// the one kind whose stored form (a `{columns, rows}` spec) is not the useful
// form, so it is re-serialised here.

import type { AssistantArtifact } from "@agora/core/types";
import { parseTableSpec, toArtifactKind } from "./artifact";
import type { TableCell } from "./artifact";

export interface ArtifactExport {
  filename: string;
  /** Media type WITHOUT a charset — the caller appends one for the Blob. */
  mimeType: string;
  body: string;
}

/** RFC 4180 line separator. Excel, Numbers and LibreOffice all read it. */
const CRLF = "\r\n";

// A leading =, +, @, tab or CR turns a CSV cell into a formula when the file
// is opened in a spreadsheet. Artifact content is model-generated and can be
// steered by issue text the model read, so a downloaded table must not be able
// to execute on open. Prefixing an apostrophe is the standard neutralisation;
// it applies to text cells only, so numbers (including negatives) round-trip
// untouched.
const FORMULA_PREFIXES = ["=", "+", "@", "\t", "\r"];

function csvField(cell: TableCell | undefined): string {
  if (cell === null || cell === undefined) return "";
  if (typeof cell === "number") return Number.isFinite(cell) ? String(cell) : "";

  const neutralised = FORMULA_PREFIXES.some((prefix) => cell.startsWith(prefix))
    ? `'${cell}`
    : cell;
  // Quote when the value carries a separator, a quote, a newline, or edge
  // whitespace a naive reader would trim away.
  const needsQuotes = /[",\r\n]/.test(neutralised) || neutralised !== neutralised.trim();
  return needsQuotes ? `"${neutralised.replace(/"/g, '""')}"` : neutralised;
}

/**
 * Serialise a table spec to CSV. Short rows are padded so every line has the
 * same field count — a ragged row from a drifted spec would otherwise shift
 * every later column in the reader.
 */
export function toCsv(columns: string[], rows: TableCell[][]): string {
  const lines = [columns.map((column) => csvField(column)).join(",")];
  for (const row of rows) {
    lines.push(columns.map((_column, index) => csvField(row[index])).join(","));
  }
  return lines.join(CRLF);
}

/**
 * A filename safe on every platform, derived from the artifact title.
 * Punctuation the three OS families disagree about is replaced rather than
 * dropped, so two artifacts whose titles differ only in punctuation still
 * produce different files.
 */
export function artifactFileName(title: string, extension: string): string {
  const base = title.replace(/[^\p{L}\p{N} _-]/gu, "_").trim().slice(0, 100).trim();
  return `${base || "artifact"}.${extension}`;
}

export function artifactExport(artifact: AssistantArtifact): ArtifactExport {
  const name = (extension: string, mimeType: string, body: string): ArtifactExport => ({
    filename: artifactFileName(artifact.title, extension),
    mimeType,
    body,
  });

  switch (toArtifactKind(artifact.kind)) {
    case "markdown":
      return name("md", "text/markdown", artifact.content);
    case "html":
      return name("html", "text/html", artifact.content);
    case "chart":
      return name("json", "application/json", artifact.content);
    case "table": {
      const spec = parseTableSpec(artifact.content);
      // A spec this build can't read is already shown as raw content in the
      // pane; exporting it as JSON keeps the download honest instead of
      // handing over a .csv that is really a JSON blob.
      return spec
        ? name("csv", "text/csv", toCsv(spec.columns, spec.rows))
        : name("json", "application/json", artifact.content);
    }
    default:
      // An unknown future kind: ship the bytes as they came (enum drift
      // downgrades, never blocks the action).
      return name("txt", "text/plain", artifact.content);
  }
}
