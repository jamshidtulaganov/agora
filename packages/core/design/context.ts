import { z } from "zod";

export const DesignContextSourceSchema = z.object({
  kind: z.string().default(""),
  locator: z.string().default(""),
  revision: z.string().default(""),
  content_hash: z.string().default(""),
  captured_at: z.string().default(""),
}).loose();

export const DesignContextDocumentSchema = z.object({
  version: z.number().default(1),
  kind: z.string().default("inventory"),
  figma: z.object({
    library_file_key: z.string().default(""),
    notes: z.string().default(""),
  }).loose().default({ library_file_key: "", notes: "" }),
  tokens: z.object({
    colors: z.record(z.string(), z.string()).default({}),
    typography: z.record(z.string(), z.string()).default({}),
    spacing: z.record(z.string(), z.string()).default({}),
  }).loose().default({ colors: {}, typography: {}, spacing: {} }),
  components: z.array(z.object({
    name: z.string().default(""),
    code_ref: z.string().default(""),
    figma_node_id: z.string().nullable().default(null),
    usage: z.string().default(""),
  }).loose()).default([]),
  conventions: z.array(z.string()).default([]),
  anti_patterns: z.array(z.string()).default([]),
  legacy_notes: z.string().default(""),
  screens_reference: z.string().default(""),
  sources: z.array(DesignContextSourceSchema).default([]),
}).loose();

export const DesignContextRevisionSchema = z.object({
  id: z.string().default(""),
  revision: z.number().default(0),
  base_revision: z.number().default(0),
  status: z.string().default(""),
  context: DesignContextDocumentSchema,
  context_hash: z.string().default(""),
  source_hash: z.string().default(""),
  freshness: z.object({
    status: z.string().default("unverified"),
    stale_sources: z.array(z.string()).default([]),
  }).loose().default({ status: "unverified", stale_sources: [] }),
  proposed_by_type: z.string().default(""),
  proposed_by_id: z.string().nullable().default(null),
  reviewed_by: z.string().nullable().default(null),
  generated_at: z.string().nullable().default(null),
  proposed_at: z.string().default(""),
  reviewed_at: z.string().nullable().default(null),
  rejection_reason: z.string().default(""),
}).loose();

export const DesignContextStateSchema = z.object({
  active: DesignContextRevisionSchema.nullable().default(null),
  proposal: DesignContextRevisionSchema.nullable().default(null),
  history: z.array(DesignContextRevisionSchema).default([]),
  effective: DesignContextDocumentSchema.optional(),
}).loose();

export type DesignContextDocument = z.infer<typeof DesignContextDocumentSchema>;
export type DesignContextRevision = z.infer<typeof DesignContextRevisionSchema>;
export type DesignContextState = z.infer<typeof DesignContextStateSchema>;

export const EMPTY_DESIGN_CONTEXT_REVISION: DesignContextRevision = {
  id: "",
  revision: 0,
  base_revision: 0,
  status: "",
  context: DesignContextDocumentSchema.parse({}),
  context_hash: "",
  source_hash: "",
  freshness: { status: "unverified", stale_sources: [] },
  proposed_by_type: "",
  proposed_by_id: null,
  reviewed_by: null,
  generated_at: null,
  proposed_at: "",
  reviewed_at: null,
  rejection_reason: "",
};

export const EMPTY_DESIGN_CONTEXT_STATE: DesignContextState = {
  active: null,
  proposal: null,
  history: [],
};
