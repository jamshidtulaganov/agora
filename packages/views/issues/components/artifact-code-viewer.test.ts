import { describe, expect, it } from "vitest";
import { artifactLanguage } from "./artifact-code-viewer";

describe("artifactLanguage", () => {
  it("maps common project files to Shiki languages", () => {
    expect(artifactLanguage("services/agentDwh.js")).toBe("javascript");
    expect(artifactLanguage("src/component.tsx")).toBe("tsx");
    expect(artifactLanguage("Dockerfile")).toBe("dockerfile");
    expect(artifactLanguage("Makefile")).toBe("makefile");
  });

  it("falls back to plain text for unknown files", () => {
    expect(artifactLanguage("fixtures/sample.custom")).toBe("text");
  });
});
