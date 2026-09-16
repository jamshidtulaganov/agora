import { describe, it, expect } from "vitest";
import {
  isArtifactToolName,
  parseArtifactToolResult,
  parseChartSpec,
  parseTableSpec,
  toArtifactKind,
} from "./artifact";

describe("isArtifactToolName", () => {
  it("matches only the two artifact tools", () => {
    expect(isArtifactToolName("create_artifact")).toBe(true);
    expect(isArtifactToolName("update_artifact")).toBe(true);
    expect(isArtifactToolName("search_issues")).toBe(false);
    expect(isArtifactToolName(null)).toBe(false);
    expect(isArtifactToolName(undefined)).toBe(false);
  });
});

describe("parseArtifactToolResult", () => {
  it("reads the documented {artifact_id, title, kind, version} shape", () => {
    expect(
      parseArtifactToolResult({
        artifact_id: "art-1",
        title: "Usage by day",
        kind: "chart",
        version: 3,
      }),
    ).toEqual({ artifactId: "art-1", title: "Usage by day", kind: "chart", version: 3 });
  });

  it("defaults a missing version/title rather than rejecting the card", () => {
    expect(parseArtifactToolResult({ artifact_id: "art-1", kind: "table" })).toEqual({
      artifactId: "art-1",
      title: "",
      kind: "table",
      version: 1,
    });
  });

  it("returns null for shapes the transcript can't turn into a card", () => {
    // Each of these degrades the row to the plain tool chip.
    expect(parseArtifactToolResult(null)).toBeNull();
    expect(parseArtifactToolResult("ok")).toBeNull();
    expect(parseArtifactToolResult([{ artifact_id: "art-1" }])).toBeNull();
    expect(parseArtifactToolResult({ error: "size limit exceeded" })).toBeNull();
    expect(parseArtifactToolResult({ artifact_id: "" })).toBeNull();
    expect(parseArtifactToolResult({ artifact_id: 42 })).toBeNull();
  });
});

describe("toArtifactKind", () => {
  it("narrows known kinds and rejects a future one", () => {
    expect(toArtifactKind("chart")).toBe("chart");
    expect(toArtifactKind("html")).toBe("html");
    expect(toArtifactKind("mermaid")).toBeNull();
    expect(toArtifactKind(undefined)).toBeNull();
  });
});

describe("parseChartSpec", () => {
  const spec = {
    type: "bar",
    x: "day",
    series: [{ key: "runs", label: "Runs" }, { key: "fails" }],
    rows: [{ day: "Mon", runs: 3, fails: 1 }],
  };

  it("parses a well-formed spec", () => {
    expect(parseChartSpec(JSON.stringify(spec))).toEqual({
      type: "bar",
      x: "day",
      series: [{ key: "runs", label: "Runs" }, { key: "fails", label: undefined }],
      rows: [{ day: "Mon", runs: 3, fails: 1 }],
    });
  });

  it("accepts an empty rows array (a chart with no data yet)", () => {
    expect(parseChartSpec(JSON.stringify({ ...spec, rows: [] }))?.rows).toEqual([]);
  });

  it("returns null for every malformed spec so the pane can downgrade", () => {
    expect(parseChartSpec("")).toBeNull();
    expect(parseChartSpec("not json")).toBeNull();
    expect(parseChartSpec("[1,2,3]")).toBeNull();
    // Unknown chart type — a newer server's type we have no renderer for.
    expect(parseChartSpec(JSON.stringify({ ...spec, type: "radar" }))).toBeNull();
    expect(parseChartSpec(JSON.stringify({ ...spec, x: "" }))).toBeNull();
    expect(parseChartSpec(JSON.stringify({ ...spec, series: [] }))).toBeNull();
    expect(parseChartSpec(JSON.stringify({ ...spec, series: [{ label: "no key" }] }))).toBeNull();
    expect(parseChartSpec(JSON.stringify({ ...spec, rows: "nope" }))).toBeNull();
  });
});

describe("parseTableSpec", () => {
  it("parses a well-formed spec", () => {
    expect(
      parseTableSpec(JSON.stringify({ columns: ["Issue", "Age"], rows: [["MUL-1", 3]] })),
    ).toEqual({ columns: ["Issue", "Age"], rows: [["MUL-1", 3]] });
  });

  it("nulls a cell of an unexpected type instead of dropping the table", () => {
    const parsed = parseTableSpec(
      JSON.stringify({ columns: ["a"], rows: [[{ nested: true }], [null], [false]] }),
    );
    expect(parsed?.rows).toEqual([[null], [null], [null]]);
  });

  it("returns null for malformed specs", () => {
    expect(parseTableSpec("{}")).toBeNull();
    expect(parseTableSpec(JSON.stringify({ columns: [], rows: [] }))).toBeNull();
    expect(parseTableSpec(JSON.stringify({ columns: [1, 2], rows: [] }))).toBeNull();
    expect(parseTableSpec(JSON.stringify({ columns: ["a"], rows: "nope" }))).toBeNull();
    expect(parseTableSpec(JSON.stringify({ columns: ["a"], rows: [{ a: 1 }] }))).toBeNull();
  });
});
