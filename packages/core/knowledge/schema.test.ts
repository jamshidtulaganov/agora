import { describe, expect, it } from "vitest";
import {
  parseKnowledgeDocDetailResponse,
  parseKnowledgeDocResponse,
  parseKnowledgeListResponse,
  parseKnowledgeSearchResponse,
} from "./schema";
import {
  EMPTY_KNOWLEDGE_DETAIL,
  EMPTY_KNOWLEDGE_DOC,
  EMPTY_KNOWLEDGE_LIST,
  checkKnowledgeFile,
  knowledgeFileKind,
  knowledgeViewerHref,
  normalizeKnowledgeStatus,
  parseKnowledgeSection,
} from "./types";

// Contract tests for the /api/knowledge wire schemas (CLAUDE.md "API Response
// Compatibility"): each malformed or drifted payload goes through the same
// parser the ApiClient uses and must degrade, never throw.

const doc = {
  id: "doc-1",
  title: "Collections SOP",
  source: "upload",
  filename: "collections-sop.pdf",
  content_type: "application/pdf",
  size_bytes: 2048,
  status: "ready",
  pinned: true,
  page_count: 12,
  chunk_count: 30,
  attachment_id: "att-1",
  created_by_name: "Dilnoza",
  created_at: "2026-09-25T10:00:00Z",
  updated_at: "2026-09-25T10:05:00Z",
};

describe("parseKnowledgeListResponse", () => {
  it("parses a full list", () => {
    const parsed = parseKnowledgeListResponse({ documents: [doc], can_manage: true });
    expect(parsed.can_manage).toBe(true);
    expect(parsed.documents).toEqual([doc]);
  });

  it("drops one malformed row instead of emptying the whole page", () => {
    const parsed = parseKnowledgeListResponse({
      documents: [doc, { title: "no id" }, null, "nope", { ...doc, id: "doc-2" }],
      can_manage: false,
    });
    expect(parsed.documents.map((d) => d.id)).toEqual(["doc-1", "doc-2"]);
  });

  it("defaults missing fields and reads an unknown status as failed", () => {
    const parsed = parseKnowledgeListResponse({
      documents: [{ id: "doc-3", status: "indexing_v2", pinned: "yes", chunk_count: null }],
    });
    expect(parsed.can_manage).toBe(false);
    expect(parsed.documents[0]).toMatchObject({
      id: "doc-3",
      title: "",
      source: "upload",
      status: "failed",
      pinned: false,
      chunk_count: 0,
      created_at: "",
    });
    expect(parsed.documents[0]?.attachment_id).toBeUndefined();
  });

  it("treats a null documents array as empty", () => {
    expect(parseKnowledgeListResponse({ documents: null, can_manage: true })).toEqual({
      documents: [],
      can_manage: true,
    });
  });

  it("falls back to an empty list for a non-object body", () => {
    expect(parseKnowledgeListResponse(null)).toEqual(EMPTY_KNOWLEDGE_LIST);
    expect(parseKnowledgeListResponse("<html>")).toEqual(EMPTY_KNOWLEDGE_LIST);
  });

  it("never reports can_manage from a wrong-typed field", () => {
    expect(parseKnowledgeListResponse({ documents: [], can_manage: "true" }).can_manage).toBe(false);
  });
});

describe("parseKnowledgeDocResponse", () => {
  it("parses a created document", () => {
    const parsed = parseKnowledgeDocResponse({ ...doc, status: "processing" }, "test");
    expect(parsed.status).toBe("processing");
    expect(parsed.attachment_id).toBe("att-1");
  });

  it("falls back to an id-less doc when the id is missing", () => {
    expect(parseKnowledgeDocResponse({ title: "x" }, "test")).toEqual(EMPTY_KNOWLEDGE_DOC);
    expect(parseKnowledgeDocResponse(undefined, "test").id).toBe("");
  });
});

describe("parseKnowledgeDocDetailResponse", () => {
  it("returns sections in reading order", () => {
    const parsed = parseKnowledgeDocDetailResponse({
      document: doc,
      chunks: [
        { id: "c2", ord: 1, heading_path: "SOP › Approval", location: "p. 4", body: "Second" },
        { id: "c1", ord: 0, heading_path: "SOP", location: "p. 1", body: "First" },
      ],
    });
    expect(parsed.document.id).toBe("doc-1");
    expect(parsed.chunks.map((c) => c.body)).toEqual(["First", "Second"]);
  });

  it("fills a missing ord from the position and joins an array breadcrumb", () => {
    const parsed = parseKnowledgeDocDetailResponse({
      document: doc,
      chunks: [
        { id: "c1", heading_path: ["SOP", "Write-offs"], body: "Body" },
        { id: "c2", ord: "x", heading_path: 5, location: null, body: null },
        "garbage",
      ],
    });
    expect(parsed.chunks).toEqual([
      { id: "c1", ord: 0, heading_path: "SOP › Write-offs", location: "", body: "Body" },
      { id: "c2", ord: 1, heading_path: "", location: "", body: "" },
    ]);
  });

  it("falls back when the document itself is malformed", () => {
    expect(parseKnowledgeDocDetailResponse({ document: null, chunks: [] })).toEqual(
      EMPTY_KNOWLEDGE_DETAIL,
    );
    expect(parseKnowledgeDocDetailResponse([])).toEqual(EMPTY_KNOWLEDGE_DETAIL);
  });

  it("treats a null chunks array as no sections", () => {
    expect(parseKnowledgeDocDetailResponse({ document: doc, chunks: null }).chunks).toEqual([]);
  });
});

describe("parseKnowledgeSearchResponse", () => {
  it("parses results and keeps the section ord when present", () => {
    const parsed = parseKnowledgeSearchResponse({
      results: [
        {
          chunk_id: "c1",
          doc_id: "doc-1",
          doc_title: "Collections SOP",
          section: 3,
          heading_path: "SOP › Write-offs",
          location: "p. 4",
          snippet: "Write-offs above $500 need approval",
          cite: "kb:abcd1234",
        },
      ],
    });
    expect(parsed.results[0]).toMatchObject({ doc_id: "doc-1", section: 3, cite: "kb:abcd1234" });
  });

  it("drops results without a document and defaults the rest", () => {
    const parsed = parseKnowledgeSearchResponse({
      results: [{ chunk_id: "c9", snippet: "orphan" }, { doc_id: "doc-2", section: -1 }],
    });
    expect(parsed.results).toEqual([
      {
        chunk_id: "",
        doc_id: "doc-2",
        doc_title: "",
        heading_path: "",
        location: "",
        snippet: "",
        cite: "",
      },
    ]);
  });

  it("falls back to no results for a null or wrong-typed body", () => {
    expect(parseKnowledgeSearchResponse(null)).toEqual({ results: [] });
    expect(parseKnowledgeSearchResponse({ results: "none" })).toEqual({ results: [] });
  });
});

describe("knowledge helpers", () => {
  it("normalizes statuses", () => {
    expect(normalizeKnowledgeStatus("needs_ocr")).toBe("needs_ocr");
    expect(normalizeKnowledgeStatus("queued")).toBe("failed");
    expect(normalizeKnowledgeStatus(undefined)).toBe("failed");
  });

  it("checks files before upload", () => {
    expect(checkKnowledgeFile({ name: "SOP.PDF", size: 10 })).toBeNull();
    expect(checkKnowledgeFile({ name: "rates.xlsx", size: 10 })).toBeNull();
    expect(checkKnowledgeFile({ name: "photo.png", size: 10 })).toBe("unsupported_type");
    expect(checkKnowledgeFile({ name: "README", size: 10 })).toBe("unsupported_type");
    expect(checkKnowledgeFile({ name: "big.pdf", size: 25 * 1024 * 1024 + 1 })).toBe("too_large");
    expect(checkKnowledgeFile({ name: "empty.txt", size: 0 })).toBe("empty");
  });

  it("names a document's kind for its icon", () => {
    expect(knowledgeFileKind({ source: "note" })).toBe("note");
    expect(knowledgeFileKind({ source: "upload", filename: "a.pdf" })).toBe("pdf");
    expect(knowledgeFileKind({ source: "upload", filename: "a.docx" })).toBe("word");
    expect(knowledgeFileKind({ source: "upload", filename: "a.csv" })).toBe("sheet");
    expect(knowledgeFileKind({ source: "upload", filename: "a.md" })).toBe("text");
    expect(knowledgeFileKind({ source: "upload" })).toBe("text");
  });

  it("builds and reads viewer deep links", () => {
    expect(knowledgeViewerHref("/acme/knowledge", "doc-1", 4)).toBe(
      "/acme/knowledge?doc=doc-1&section=4",
    );
    expect(knowledgeViewerHref("/acme/knowledge", "doc-1")).toBe("/acme/knowledge?doc=doc-1");
    expect(knowledgeViewerHref("/acme/knowledge", "doc-1", -2)).toBe("/acme/knowledge?doc=doc-1");
    expect(parseKnowledgeSection("7")).toBe(7);
    expect(parseKnowledgeSection("0")).toBe(0);
    expect(parseKnowledgeSection("-1")).toBeNull();
    expect(parseKnowledgeSection("abc")).toBeNull();
    expect(parseKnowledgeSection(null)).toBeNull();
  });
});
