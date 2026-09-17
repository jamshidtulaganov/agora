import { describe, expect, it } from "vitest";
import type { AssistantArtifact } from "@agora/core/types";
import { artifactExport, artifactFileName, toCsv } from "./artifact-export";

function artifact(overrides: Partial<AssistantArtifact>): AssistantArtifact {
  return {
    id: "art-1",
    session_id: "session-1",
    title: "Sprint report",
    kind: "markdown",
    content: "# Hello",
    version: 1,
    created_at: "2026-09-16T10:00:00Z",
    updated_at: "2026-09-16T10:00:00Z",
    ...overrides,
  };
}

describe("toCsv", () => {
  it("quotes only the fields that need it", () => {
    const csv = toCsv(
      ["Issue", "Title"],
      [
        ["MUL-1", "plain"],
        ["MUL-2", "has, a comma"],
        ["MUL-3", 'has "quotes"'],
        ["MUL-4", "has\nnewline"],
      ],
    );

    expect(csv.split("\r\n")).toEqual([
      "Issue,Title",
      "MUL-1,plain",
      'MUL-2,"has, a comma"',
      'MUL-3,"has ""quotes"""',
      'MUL-4,"has\nnewline"',
    ]);
  });

  it("writes null and undefined cells as empty fields, not the word null", () => {
    expect(toCsv(["A", "B", "C"], [[null, 0, "x"]])).toBe("A,B,C\r\n,0,x");
    // A short row (drifted spec) is padded so later columns don't shift.
    expect(toCsv(["A", "B", "C"], [["only"]])).toBe("A,B,C\r\nonly,,");
  });

  it("keeps numbers unquoted and drops non-finite ones", () => {
    expect(toCsv(["N"], [[3], [-4.5], [Number.NaN], [Number.POSITIVE_INFINITY]])).toBe(
      "N\r\n3\r\n-4.5\r\n\r\n",
    );
  });

  it("preserves edge whitespace by quoting it", () => {
    expect(toCsv(["A"], [[" padded "]])).toBe('A\r\n" padded "');
  });

  it("neutralises spreadsheet formulas in text cells", () => {
    // Artifact content is model-written and can be steered by whatever the
    // model read; a downloaded table must not execute when it is opened.
    expect(toCsv(["A"], [["=SUM(A1:A2)"], ["@cmd"], ["+1-1"]])).toBe(
      "A\r\n'=SUM(A1:A2)\r\n'@cmd\r\n'+1-1",
    );
  });
});

describe("artifactFileName", () => {
  it("replaces path and shell punctuation but keeps readable titles", () => {
    expect(artifactFileName("Q3 report: agents/runs", "csv")).toBe("Q3 report_ agents_runs.csv");
  });

  it("falls back to a generic name when the title is empty or all punctuation", () => {
    expect(artifactFileName("", "md")).toBe("artifact.md");
    expect(artifactFileName("///", "md")).toBe("___.md");
    expect(artifactFileName("   ", "md")).toBe("artifact.md");
  });
});

describe("artifactExport", () => {
  it("maps each kind to the format a person would open it in", () => {
    expect(artifactExport(artifact({ kind: "markdown", content: "# H" }))).toMatchObject({
      filename: "Sprint report.md",
      mimeType: "text/markdown",
      body: "# H",
    });
    expect(artifactExport(artifact({ kind: "html", content: "<p>x</p>" }))).toMatchObject({
      filename: "Sprint report.html",
      mimeType: "text/html",
    });
    expect(artifactExport(artifact({ kind: "chart", content: '{"type":"bar"}' }))).toMatchObject({
      filename: "Sprint report.json",
      mimeType: "application/json",
      body: '{"type":"bar"}',
    });
  });

  it("re-serialises a table spec into CSV", () => {
    const result = artifactExport(
      artifact({
        title: "Open issues",
        kind: "table",
        content: JSON.stringify({ columns: ["A", "B"], rows: [["x", 1]] }),
      }),
    );

    expect(result.filename).toBe("Open issues.csv");
    expect(result.mimeType).toBe("text/csv");
    expect(result.body).toBe("A,B\r\nx,1");
  });

  it("exports a table whose spec this build can't read as raw JSON, not a fake CSV", () => {
    const result = artifactExport(artifact({ kind: "table", content: "{not a spec" }));

    expect(result.filename).toBe("Sprint report.json");
    expect(result.body).toBe("{not a spec");
  });

  it("ships an unknown future kind as plain text rather than refusing", () => {
    const result = artifactExport(artifact({ kind: "mermaid", content: "graph TD; A-->B;" }));

    expect(result.filename).toBe("Sprint report.txt");
    expect(result.mimeType).toBe("text/plain");
    expect(result.body).toBe("graph TD; A-->B;");
  });
});
