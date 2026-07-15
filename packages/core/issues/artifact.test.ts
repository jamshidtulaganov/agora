import { describe, expect, it } from "vitest";
import {
  parseArtifactChangesResponse,
  parseArtifactChecksResponse,
  parseArtifactFileResponse,
  parseArtifactPreviewResponse,
} from "./artifact";

describe("artifact daemon response parsers", () => {
  it("accepts exact Git-backed changes and defaults optional collections", () => {
    expect(parseArtifactChangesResponse({
      artifact_id: "artifact-1",
      repos: [{
        repo: "agora",
        base_sha: "a".repeat(40),
        head_sha: "b".repeat(40),
        files: [{ path: "src/app.ts", additions: 2, deletions: 1 }],
      }],
    })).toMatchObject({
      artifact_id: "artifact-1",
      repos: [{ repo: "agora", tree: [], diff: "", truncated: false }],
    });
  });

  it("fails closed when the changes body is malformed", () => {
    expect(parseArtifactChangesResponse({ artifact_id: 42, repos: null })).toEqual({
      artifact_id: "",
      repos: [],
    });
  });

  it("fails closed when a file body does not carry identity", () => {
    expect(parseArtifactFileResponse({ content: "unbound" })).toMatchObject({
      repo: "",
      path: "",
      head_sha: "",
      content: "",
    });
  });

  it("parses preview and check evidence without accepting extra authority", () => {
    expect(parseArtifactPreviewResponse({
      artifact_id: "artifact-1",
      running: true,
      url: "http://127.0.0.1:3001",
      source_root: "/private/source",
    })).toEqual({
      artifact_id: "artifact-1",
      running: true,
      url: "http://127.0.0.1:3001",
      needs_command: false,
    });
    expect(parseArtifactChecksResponse({
      artifact_id: "artifact-1",
      head_sha: "a".repeat(40),
      passed: true,
      exit_code: 0,
      output: "ok",
    })).toMatchObject({ passed: true, exit_code: 0, output: "ok" });
  });
});
