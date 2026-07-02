import { z } from "zod";

// The project design manifest (project.settings.design_manifest) — read
// leniently the same way the API schemas are: every field defaults, unknown
// kinds/sources render generically, and a malformed manifest degrades to an
// empty one rather than crashing the project settings view. The manifest is
// injected server-side into agent prompts; this schema is the frontend read
// contract for the Project → Design editor.

export type DesignManifestKind = "tokens" | "inventory";
export type DesignManifestSource = "agent" | "manual" | "mixed";

const ComponentSchema = z
  .object({
    name: z.string().default(""),
    code_ref: z.string().default(""),
    figma_node_id: z.string().nullable().default(null),
    usage: z.string().default(""),
  })
  .loose();

export const DesignManifestSchema = z
  .object({
    kind: z
      .string()
      .default("inventory")
      .transform((v): DesignManifestKind => (v === "tokens" ? "tokens" : "inventory")),
    source: z
      .string()
      .default("agent")
      .transform((v): DesignManifestSource =>
        v === "manual" || v === "mixed" || v === "agent" ? v : "agent",
      ),
    revision: z.number().default(0),
    updated_at: z.string().default(""),
    figma: z
      .object({
        library_file_key: z.string().default(""),
        notes: z.string().default(""),
      })
      .loose()
      .default({ library_file_key: "", notes: "" }),
    tokens: z
      .object({
        colors: z.record(z.string(), z.string()).default({}),
        typography: z.record(z.string(), z.string()).default({}),
        spacing: z.record(z.string(), z.string()).default({}),
      })
      .loose()
      .default({ colors: {}, typography: {}, spacing: {} }),
    components: z.array(ComponentSchema).default([]),
    conventions: z.array(z.string()).default([]),
    anti_patterns: z.array(z.string()).default([]),
    legacy_notes: z.string().default(""),
    screens_reference: z.string().default(""),
  })
  .loose();

export type DesignManifest = z.infer<typeof DesignManifestSchema>;

// parseDesignManifest reads a raw settings value (which may be undefined or any
// shape) into a typed manifest, or null when there is none / it is unparseable.
export function parseDesignManifest(raw: unknown): DesignManifest | null {
  if (raw == null || typeof raw !== "object") return null;
  const result = DesignManifestSchema.safeParse(raw);
  return result.success ? result.data : null;
}
