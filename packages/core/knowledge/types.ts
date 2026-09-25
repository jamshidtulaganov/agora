// Workspace knowledge base types. Mirror the backend payloads of the
// /api/knowledge endpoints (server/internal/handler/knowledge.go,
// docs/workspace-knowledge-plan.md §5). Wire fields stay snake_case like
// every other API type in this package.

/** What the reader did with a document. Anything else is shown as "failed". */
export type KnowledgeDocStatus = "processing" | "ready" | "failed" | "needs_ocr";

export const KNOWLEDGE_DOC_STATUSES: readonly KnowledgeDocStatus[] = [
  "processing",
  "ready",
  "failed",
  "needs_ocr",
];

/** A file someone uploaded, or a note written on the page. */
export type KnowledgeDocSource = "upload" | "note";

export interface KnowledgeDoc {
  id: string;
  title: string;
  /** "upload" | "note". Kept as string: a newer server may add a source. */
  source: string;
  filename?: string;
  content_type?: string;
  size_bytes?: number;
  status: KnowledgeDocStatus;
  /** Why the reader could not read the file, for "failed" / "needs_ocr". */
  error?: string;
  /** Pinned documents are always given to the Assistant and agents. */
  pinned: boolean;
  page_count?: number;
  chunk_count: number;
  /** The uploaded original, present for source "upload". */
  attachment_id?: string;
  created_by_name?: string;
  created_at: string;
  updated_at: string;
}

export interface KnowledgeListResponse {
  documents: KnowledgeDoc[];
  /** True when the caller is a workspace owner or admin. */
  can_manage: boolean;
}

/** One section of a document, in reading order. */
export interface KnowledgeChunk {
  id: string;
  ord: number;
  /** Breadcrumb of headings, e.g. "Collections SOP › Write-offs". */
  heading_path: string;
  /** Where the section came from, e.g. "p. 4" or `Sheet "Rates", rows 2–41`. */
  location: string;
  /** Markdown body. */
  body: string;
}

export interface KnowledgeDocDetail {
  document: KnowledgeDoc;
  chunks: KnowledgeChunk[];
}

export interface KnowledgeSearchResult {
  chunk_id: string;
  doc_id: string;
  doc_title: string;
  /** The section's ord, when the server sends it. */
  section?: number;
  heading_path: string;
  location: string;
  snippet: string;
  cite: string;
}

export interface KnowledgeSearchResponse {
  results: KnowledgeSearchResult[];
}

/** POST /api/knowledge: an uploaded attachment, or a Markdown note. */
export type CreateKnowledgeDocRequest =
  | { attachment_id: string }
  | { title: string; body: string };

export interface UpdateKnowledgeDocRequest {
  title?: string;
  pinned?: boolean;
}

/** WS `knowledge:updated` payload. */
export interface KnowledgeUpdatedPayload {
  doc_id: string;
  status: string;
}

export const EMPTY_KNOWLEDGE_DOC: KnowledgeDoc = {
  id: "",
  title: "",
  source: "upload",
  status: "failed",
  pinned: false,
  chunk_count: 0,
  created_at: "",
  updated_at: "",
};

export const EMPTY_KNOWLEDGE_LIST: KnowledgeListResponse = {
  documents: [],
  can_manage: false,
};

export const EMPTY_KNOWLEDGE_DETAIL: KnowledgeDocDetail = {
  document: EMPTY_KNOWLEDGE_DOC,
  chunks: [],
};

export const EMPTY_KNOWLEDGE_SEARCH: KnowledgeSearchResponse = { results: [] };

/** Server-driven status → one of the four the UI knows; unknown reads as failed. */
export function normalizeKnowledgeStatus(status: unknown): KnowledgeDocStatus {
  return typeof status === "string" &&
    (KNOWLEDGE_DOC_STATUSES as readonly string[]).includes(status)
    ? (status as KnowledgeDocStatus)
    : "failed";
}

// --- Client-side upload checks ------------------------------------------------
// Mirror the server's limits (knowledge.go) so a wrong file is refused before
// it is uploaded at all. The server stays the authority: it answers 413 / 415.

export const KNOWLEDGE_MAX_FILE_BYTES = 25 * 1024 * 1024;

export const KNOWLEDGE_FILE_EXTENSIONS = [
  ".pdf",
  ".docx",
  ".xlsx",
  ".csv",
  ".md",
  ".markdown",
  ".txt",
] as const;

/** Value for an `<input type="file" accept>`. */
export const KNOWLEDGE_ACCEPT = KNOWLEDGE_FILE_EXTENSIONS.join(",");

export type KnowledgeFileProblem = "unsupported_type" | "too_large" | "empty";

export function knowledgeFileExtension(filename: string): string {
  const dot = filename.lastIndexOf(".");
  return dot >= 0 ? filename.slice(dot).toLowerCase() : "";
}

/** Null when the file can be uploaded, otherwise why not. */
export function checkKnowledgeFile(file: { name: string; size: number }): KnowledgeFileProblem | null {
  if (!(KNOWLEDGE_FILE_EXTENSIONS as readonly string[]).includes(knowledgeFileExtension(file.name))) {
    return "unsupported_type";
  }
  if (file.size > KNOWLEDGE_MAX_FILE_BYTES) return "too_large";
  if (file.size === 0) return "empty";
  return null;
}

/** What kind of file a document is, for its icon. */
export type KnowledgeFileKind = "pdf" | "word" | "sheet" | "text" | "note";

export function knowledgeFileKind(doc: Pick<KnowledgeDoc, "source" | "filename" | "content_type">): KnowledgeFileKind {
  if (doc.source === "note") return "note";
  const ext = knowledgeFileExtension(doc.filename ?? "");
  const type = (doc.content_type ?? "").toLowerCase();
  if (ext === ".pdf" || type === "application/pdf") return "pdf";
  if (ext === ".docx" || type.includes("wordprocessingml")) return "word";
  if (ext === ".xlsx" || ext === ".csv" || type.includes("spreadsheetml") || type === "text/csv") {
    return "sheet";
  }
  return "text";
}

/** The viewer's deep-link search params: `?doc=<id>&section=<ord>`. */
export const KNOWLEDGE_DOC_PARAM = "doc";
export const KNOWLEDGE_SECTION_PARAM = "section";

/** `/<slug>/knowledge?doc=<id>&section=<ord>` from a knowledge page path. */
export function knowledgeViewerHref(knowledgePath: string, docId: string, section?: number | null): string {
  const params = new URLSearchParams({ [KNOWLEDGE_DOC_PARAM]: docId });
  if (typeof section === "number" && Number.isInteger(section) && section >= 0) {
    params.set(KNOWLEDGE_SECTION_PARAM, String(section));
  }
  return `${knowledgePath}?${params.toString()}`;
}

/** Parses the `section` param; null when absent or not a non-negative integer. */
export function parseKnowledgeSection(value: string | null | undefined): number | null {
  if (value == null || !/^\d+$/.test(value)) return null;
  const n = Number(value);
  return Number.isSafeInteger(n) ? n : null;
}
