-- Workspace knowledge base (docs/workspace-knowledge-plan.md, Part A).
-- Owners/admins upload what the team runs on — SOPs, policies, price lists —
-- and the server reads each file into sections the Assistant and agents
-- search. Every member can read and search; retrieval is always scoped to a
-- workspace the person belongs to.

CREATE TABLE knowledge_doc (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id          uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    title                 text NOT NULL,
    source                text NOT NULL CHECK (source IN ('upload', 'note')),
    attachment_id         uuid REFERENCES attachment(id) ON DELETE SET NULL,
    filename              text NOT NULL DEFAULT '',
    content_type          text NOT NULL DEFAULT '',
    size_bytes            bigint NOT NULL DEFAULT 0,
    note_body             text NOT NULL DEFAULT '',   -- the markdown of a note (source = 'note')
    status                text NOT NULL DEFAULT 'processing'
        CHECK (status IN ('processing', 'ready', 'failed', 'needs_ocr')),
    error                 text NOT NULL DEFAULT '',
    pinned                boolean NOT NULL DEFAULT false, -- "always include" in every prompt and brief
    page_count            int NOT NULL DEFAULT 0,
    char_count            int NOT NULL DEFAULT 0,
    chunk_count           int NOT NULL DEFAULT 0,
    created_by            uuid REFERENCES "user"(id) ON DELETE SET NULL,
    processing_started_at timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    archived_at           timestamptz
);

CREATE INDEX idx_knowledge_doc_workspace ON knowledge_doc (workspace_id) WHERE archived_at IS NULL;
CREATE INDEX idx_knowledge_doc_processing ON knowledge_doc (processing_started_at) WHERE status = 'processing';

-- One section of a document, in reading order. The search vector combines
-- English and Russian stemming with plain words (Uzbek and anything else),
-- headings weighted above body text.
CREATE TABLE knowledge_chunk (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    doc_id       uuid NOT NULL REFERENCES knowledge_doc(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    ord          int NOT NULL,
    heading_path text NOT NULL DEFAULT '',
    location     text NOT NULL DEFAULT '',
    body         text NOT NULL,
    search       tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', heading_path), 'A') ||
        setweight(to_tsvector('simple', heading_path), 'A') ||
        setweight(to_tsvector('english', body), 'B') ||
        setweight(to_tsvector('russian', body), 'B') ||
        setweight(to_tsvector('simple', body), 'B')
    ) STORED,
    UNIQUE (doc_id, ord)
);

CREATE INDEX idx_knowledge_chunk_search ON knowledge_chunk USING gin (search);
CREATE INDEX idx_knowledge_chunk_workspace ON knowledge_chunk (workspace_id);

-- Every search, for the eval and for the "questions the knowledge base
-- couldn't answer" list: who asked, from where, and how many sections came
-- back.
CREATE TABLE knowledge_search_log (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id      uuid REFERENCES "user"(id) ON DELETE SET NULL,
    source       text NOT NULL CHECK (source IN ('assistant', 'agent', 'ui')),
    query        text NOT NULL,
    result_count int NOT NULL DEFAULT 0,
    top_doc_id   uuid,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_knowledge_search_log_workspace ON knowledge_search_log (workspace_id, created_at DESC);
