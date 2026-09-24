import { describe, expect, it } from "vitest";
import { isImportedWorkspace } from "./imported-workspace";

describe("isImportedWorkspace", () => {
  it("recognises a workspace the Zoho migration created", () => {
    expect(isImportedWorkspace({ settings: { zoho_project_id: "2494234000000229003" } })).toBe(true);
  });

  it("treats everything else as a workspace the person made themselves", () => {
    expect(isImportedWorkspace({ settings: {} })).toBe(false);
    expect(isImportedWorkspace({ settings: { zoho_project_id: "" } })).toBe(false);
    expect(isImportedWorkspace({ settings: { zoho_project_id: 42 } as Record<string, unknown> })).toBe(false);
    expect(isImportedWorkspace({ settings: null as unknown as Record<string, unknown> })).toBe(false);
    expect(isImportedWorkspace(null)).toBe(false);
  });
});
