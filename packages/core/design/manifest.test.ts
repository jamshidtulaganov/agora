import { describe, expect, it } from "vitest";

import { DesignManifestSchema, parseDesignManifest } from "./manifest";

describe("DesignManifestSchema", () => {
  it("parses a tokens manifest", () => {
    const r = DesignManifestSchema.safeParse({
      kind: "tokens",
      source: "agent",
      revision: 3,
      tokens: { colors: { primary: "#2563EB" } },
      components: [{ name: "DataTable", code_ref: "src/DataTable.vue", usage: "lists" }],
    });
    expect(r.success).toBe(true);
    if (r.success) {
      expect(r.data.kind).toBe("tokens");
      expect(r.data.components[0]?.name).toBe("DataTable");
      expect(r.data.tokens.colors.primary).toBe("#2563EB");
    }
  });

  it("defaults every field for an empty object", () => {
    const r = DesignManifestSchema.safeParse({});
    expect(r.success).toBe(true);
    if (r.success) {
      expect(r.data.kind).toBe("inventory");
      expect(r.data.source).toBe("agent");
      expect(r.data.components).toEqual([]);
      expect(r.data.tokens.colors).toEqual({});
      expect(r.data.conventions).toEqual([]);
    }
  });

  it("downgrades unknown kind/source to generic values", () => {
    const r = DesignManifestSchema.safeParse({ kind: "weird", source: "hacker" });
    expect(r.success).toBe(true);
    if (r.success) {
      expect(r.data.kind).toBe("inventory");
      expect(r.data.source).toBe("agent");
    }
  });

  it("keeps unknown future fields (loose)", () => {
    const r = DesignManifestSchema.safeParse({ kind: "tokens", future_field: 1 });
    expect(r.success).toBe(true);
  });
});

describe("parseDesignManifest", () => {
  it("returns null for nullish / non-object input", () => {
    expect(parseDesignManifest(undefined)).toBeNull();
    expect(parseDesignManifest(null)).toBeNull();
    expect(parseDesignManifest("nope")).toBeNull();
    expect(parseDesignManifest(42)).toBeNull();
  });

  it("returns a typed manifest for a valid object", () => {
    const m = parseDesignManifest({ kind: "inventory", legacy_notes: "copy markup" });
    expect(m?.kind).toBe("inventory");
    expect(m?.legacy_notes).toBe("copy markup");
  });
});
