import { z, type ZodType } from "zod";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_KNOWLEDGE_DETAIL,
  EMPTY_KNOWLEDGE_DOC,
  EMPTY_KNOWLEDGE_LIST,
  EMPTY_KNOWLEDGE_SEARCH,
  normalizeKnowledgeStatus,
  type KnowledgeChunk,
  type KnowledgeDoc,
  type KnowledgeDocDetail,
  type KnowledgeListResponse,
  type KnowledgeSearchResponse,
  type KnowledgeSearchResult,
} from "./types";

// Response schemas for /api/knowledge. Lenient on purpose (CLAUDE.md "API
// Response Compatibility"): every field has a fallback, a status this build
// doesn't know reads as "failed", and ONE malformed row is dropped instead of
// failing the whole list — an empty Knowledge page because a single document
// drifted would be the worst way to degrade.

/** Array whose malformed items are dropped rather than failing the parent. */
function tolerantArray<T>(item: ZodType<T>) {
  return z
    .array(z.unknown())
    .catch([])
    .transform((items) =>
      items.flatMap((raw) => {
        const parsed = item.safeParse(raw);
        return parsed.success ? [parsed.data] : [];
      }),
    );
}

const optionalString = z.string().optional().catch(undefined);
const optionalNumber = z.number().optional().catch(undefined);

/** A breadcrumb arrives as a string today; an array of headings is joined. */
const HeadingPathSchema = z
  .union([z.string(), z.array(z.string())])
  .catch("")
  .transform((value) => (Array.isArray(value) ? value.filter(Boolean).join(" › ") : value));

export const KnowledgeDocSchema = z
  .object({
    id: z.string().min(1),
    title: z.string().catch(""),
    source: z.string().catch("upload"),
    filename: optionalString,
    content_type: optionalString,
    size_bytes: optionalNumber,
    status: z.unknown().transform(normalizeKnowledgeStatus),
    error: optionalString,
    pinned: z.boolean().catch(false),
    page_count: optionalNumber,
    chunk_count: z.number().catch(0),
    attachment_id: optionalString,
    created_by_name: optionalString,
    created_at: z.string().catch(""),
    updated_at: z.string().catch(""),
  });

export const KnowledgeListResponseSchema = z.object({
  documents: tolerantArray(KnowledgeDocSchema),
  can_manage: z.boolean().catch(false),
});

const KnowledgeChunkSchema = z.object({
  id: z.string().catch(""),
  ord: z.number().int().nonnegative().optional().catch(undefined),
  heading_path: HeadingPathSchema,
  location: z.string().catch(""),
  body: z.string().catch(""),
});

export const KnowledgeDocDetailSchema = z
  .object({
    document: KnowledgeDocSchema,
    chunks: tolerantArray(KnowledgeChunkSchema),
  })
  .transform((detail): KnowledgeDocDetail => {
    // A section without an ord falls back to its position, so deep links and
    // keys still work; the result is always in reading order.
    const chunks: KnowledgeChunk[] = detail.chunks
      .map((chunk, index) => ({ ...chunk, ord: chunk.ord ?? index }))
      .sort((a, b) => a.ord - b.ord);
    return { document: detail.document, chunks };
  });

const KnowledgeSearchResultSchema = z
  .object({
    chunk_id: z.string().catch(""),
    doc_id: z.string().min(1),
    doc_title: z.string().catch(""),
    section: z.number().int().nonnegative().optional().catch(undefined),
    heading_path: HeadingPathSchema,
    location: z.string().catch(""),
    snippet: z.string().catch(""),
    cite: z.string().catch(""),
  })
  .transform((result): KnowledgeSearchResult => {
    const { section, ...rest } = result;
    return section === undefined ? rest : { ...rest, section };
  });

export const KnowledgeSearchResponseSchema = z.object({
  results: tolerantArray(KnowledgeSearchResultSchema),
});

export function parseKnowledgeListResponse(value: unknown): KnowledgeListResponse {
  return parseWithFallback(value, KnowledgeListResponseSchema, EMPTY_KNOWLEDGE_LIST, {
    endpoint: "GET /api/knowledge",
  });
}

export function parseKnowledgeDocResponse(value: unknown, endpoint: string): KnowledgeDoc {
  return parseWithFallback(value, KnowledgeDocSchema, EMPTY_KNOWLEDGE_DOC, { endpoint });
}

export function parseKnowledgeDocDetailResponse(value: unknown): KnowledgeDocDetail {
  return parseWithFallback(value, KnowledgeDocDetailSchema, EMPTY_KNOWLEDGE_DETAIL, {
    endpoint: "GET /api/knowledge/{id}",
  });
}

export function parseKnowledgeSearchResponse(value: unknown): KnowledgeSearchResponse {
  return parseWithFallback(value, KnowledgeSearchResponseSchema, EMPTY_KNOWLEDGE_SEARCH, {
    endpoint: "GET /api/knowledge/search",
  });
}
