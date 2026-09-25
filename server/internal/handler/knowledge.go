package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// The workspace knowledge base API (docs/workspace-knowledge-plan.md §5).
// Workspace-scoped like labels (X-Workspace-ID + membership middleware).
// Every member lists, reads and searches; only owners/admins add, rename,
// pin, reprocess or remove documents. Agents can't change the knowledge base.

const (
	knowledgeMaxFileBytes = 25 << 20
	knowledgeMaxNoteChars = 200_000
	knowledgeMaxDocs      = 300
)

// knowledgeExtensions are the file types the reader understands.
var knowledgeExtensions = map[string]bool{
	".pdf": true, ".docx": true, ".xlsx": true, ".csv": true,
	".md": true, ".markdown": true, ".txt": true,
}

type knowledgeDocResponse struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Source        string `json:"source"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	Pinned        bool   `json:"pinned"`
	PageCount     int    `json:"page_count,omitempty"`
	ChunkCount    int    `json:"chunk_count"`
	AttachmentID  string `json:"attachment_id,omitempty"`
	CreatedByName string `json:"created_by_name,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func knowledgeDocFromRow(d db.KnowledgeDoc, createdByName string) knowledgeDocResponse {
	resp := knowledgeDocResponse{
		ID: uuidToString(d.ID), Title: d.Title, Source: d.Source,
		Filename: d.Filename, ContentType: d.ContentType, SizeBytes: d.SizeBytes,
		Status: d.Status, Error: d.Error, Pinned: d.Pinned,
		PageCount: int(d.PageCount), ChunkCount: int(d.ChunkCount),
		CreatedByName: createdByName,
		CreatedAt:     d.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt:     d.UpdatedAt.Time.UTC().Format(time.RFC3339),
	}
	if d.AttachmentID.Valid {
		resp.AttachmentID = uuidToString(d.AttachmentID)
	}
	return resp
}

// knowledgeCanManage reports whether the caller may change the knowledge
// base: a human owner/admin of the workspace.
func (h *Handler) knowledgeCanManage(r *http.Request, wsUUID pgtype.UUID) bool {
	if r.Header.Get("X-Actor-Source") == "task_token" {
		return false
	}
	userUUID, err := knowledgeUUID(requestUserID(r))
	if err != nil {
		return false
	}
	m, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{UserID: userUUID, WorkspaceID: wsUUID})
	return err == nil && (m.Role == "owner" || m.Role == "admin")
}

func (h *Handler) knowledgeWorkspace(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	return parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
}

func (h *Handler) requireKnowledgeManager(w http.ResponseWriter, r *http.Request, wsUUID pgtype.UUID) bool {
	if !h.knowledgeCanManage(r, wsUUID) {
		writeError(w, http.StatusForbidden, "only workspace owners and admins can change the knowledge base")
		return false
	}
	return true
}

func (h *Handler) publishKnowledge(wsUUID pgtype.UUID, r *http.Request, docID pgtype.UUID, status string) {
	h.publish(protocol.EventKnowledgeUpdated, uuidToString(wsUUID), "member", requestUserID(r), map[string]any{
		"doc_id": uuidToString(docID), "status": status,
	})
}

// ListKnowledge — GET /api/knowledge.
func (h *Handler) ListKnowledge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := h.knowledgeWorkspace(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListKnowledgeDocs(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge")
		return
	}
	docs := make([]knowledgeDocResponse, 0, len(rows))
	for _, row := range rows {
		docs = append(docs, knowledgeDocFromRow(db.KnowledgeDoc{
			ID: row.ID, WorkspaceID: row.WorkspaceID, Title: row.Title, Source: row.Source,
			AttachmentID: row.AttachmentID, Filename: row.Filename, ContentType: row.ContentType,
			SizeBytes: row.SizeBytes, Status: row.Status, Error: row.Error, Pinned: row.Pinned,
			PageCount: row.PageCount, ChunkCount: row.ChunkCount, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}, row.CreatedByName))
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs, "can_manage": h.knowledgeCanManage(r, wsUUID)})
}

type createKnowledgeRequest struct {
	AttachmentID string `json:"attachment_id"`
	Title        string `json:"title"`
	Body         string `json:"body"`
}

// CreateKnowledge — POST /api/knowledge: add an uploaded file (by its
// attachment id) or a note. The document starts "processing"; reading
// happens in the background and a knowledge:updated event reports the end.
func (h *Handler) CreateKnowledge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := h.knowledgeWorkspace(w, r)
	if !ok || !h.requireKnowledgeManager(w, r, wsUUID) {
		return
	}
	var req createKnowledgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	existing, err := h.Queries.ListKnowledgeDocs(r.Context(), wsUUID)
	if err == nil && len(existing) >= knowledgeMaxDocs {
		writeError(w, http.StatusRequestEntityTooLarge, "this workspace already has the maximum number of knowledge documents")
		return
	}
	creator, _ := knowledgeUUID(requestUserID(r))
	params := db.CreateKnowledgeDocParams{WorkspaceID: wsUUID, CreatedBy: creator}

	switch {
	case strings.TrimSpace(req.AttachmentID) != "":
		attUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.AttachmentID), "attachment_id")
		if !ok {
			return
		}
		att, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{ID: attUUID, WorkspaceID: wsUUID})
		if err != nil {
			writeError(w, http.StatusNotFound, "that file wasn't found in this workspace")
			return
		}
		ext := strings.ToLower(filepath.Ext(att.Filename))
		if !knowledgeExtensions[ext] {
			writeError(w, http.StatusUnsupportedMediaType, "this file type isn't supported — use PDF, Word (.docx), Excel (.xlsx), CSV, Markdown or text")
			return
		}
		if att.SizeBytes > knowledgeMaxFileBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "this file is larger than 25 MB")
			return
		}
		params.Source = "upload"
		params.AttachmentID = attUUID
		params.Filename = att.Filename
		params.ContentType = att.ContentType
		params.SizeBytes = att.SizeBytes
		params.Title = strings.TrimSpace(req.Title)
		if params.Title == "" {
			params.Title = strings.TrimSuffix(att.Filename, filepath.Ext(att.Filename))
		}
	case strings.TrimSpace(req.Body) != "":
		if utf8.RuneCountInString(req.Body) > knowledgeMaxNoteChars {
			writeError(w, http.StatusRequestEntityTooLarge, "this note is too long")
			return
		}
		params.Source = "note"
		params.NoteBody = req.Body
		params.Title = strings.TrimSpace(req.Title)
		if params.Title == "" {
			writeError(w, http.StatusBadRequest, "a note needs a title")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "give attachment_id for a file, or title and body for a note")
		return
	}
	params.Title = clipRunes(params.Title, 200)

	doc, err := h.Queries.CreateKnowledgeDoc(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add the document")
		return
	}
	h.publishKnowledge(wsUUID, r, doc.ID, doc.Status)
	h.enqueueKnowledgeIngest(doc.ID)
	writeJSON(w, http.StatusCreated, knowledgeDocFromRow(doc, ""))
}

type knowledgeChunkResponse struct {
	ID          string `json:"id"`
	Ord         int    `json:"ord"`
	HeadingPath string `json:"heading_path"`
	Location    string `json:"location"`
	Body        string `json:"body"`
}

// GetKnowledge — GET /api/knowledge/{id}: the document and its sections,
// exactly what the AI reads.
func (h *Handler) GetKnowledge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := h.knowledgeWorkspace(w, r)
	if !ok {
		return
	}
	docUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "document id")
	if !ok {
		return
	}
	doc, err := h.Queries.GetKnowledgeDoc(r.Context(), db.GetKnowledgeDocParams{ID: docUUID, WorkspaceID: wsUUID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the document")
		return
	}
	chunks, err := h.Queries.ListKnowledgeChunks(r.Context(), db.ListKnowledgeChunksParams{DocID: docUUID, FromOrd: 0, MaxChunks: 5000})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the document")
		return
	}
	out := make([]knowledgeChunkResponse, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, knowledgeChunkResponse{ID: uuidToString(c.ID), Ord: int(c.Ord), HeadingPath: c.HeadingPath, Location: c.Location, Body: c.Body})
	}
	writeJSON(w, http.StatusOK, map[string]any{"document": knowledgeDocFromRow(doc, ""), "chunks": out})
}

type updateKnowledgeRequest struct {
	Title  *string `json:"title"`
	Pinned *bool   `json:"pinned"`
}

// UpdateKnowledge — PATCH /api/knowledge/{id}: rename or pin.
func (h *Handler) UpdateKnowledge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := h.knowledgeWorkspace(w, r)
	if !ok || !h.requireKnowledgeManager(w, r, wsUUID) {
		return
	}
	docUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "document id")
	if !ok {
		return
	}
	var req updateKnowledgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params := db.UpdateKnowledgeDocMetaParams{ID: docUUID, WorkspaceID: wsUUID}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title can't be empty")
			return
		}
		params.Title = pgtype.Text{String: clipRunes(title, 200), Valid: true}
	}
	if req.Pinned != nil {
		params.Pinned = pgtype.Bool{Bool: *req.Pinned, Valid: true}
	}
	doc, err := h.Queries.UpdateKnowledgeDocMeta(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update the document")
		return
	}
	h.publishKnowledge(wsUUID, r, doc.ID, doc.Status)
	writeJSON(w, http.StatusOK, knowledgeDocFromRow(doc, ""))
}

// DeleteKnowledge — DELETE /api/knowledge/{id}: remove the document from the
// knowledge base (the uploaded file itself is left alone).
func (h *Handler) DeleteKnowledge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := h.knowledgeWorkspace(w, r)
	if !ok || !h.requireKnowledgeManager(w, r, wsUUID) {
		return
	}
	docUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "document id")
	if !ok {
		return
	}
	n, err := h.Queries.ArchiveKnowledgeDoc(r.Context(), db.ArchiveKnowledgeDocParams{ID: docUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove the document")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err := h.Queries.DeleteKnowledgeChunksForDoc(r.Context(), docUUID); err != nil {
		slog.Warn("knowledge: delete chunks", "doc_id", uuidToString(docUUID), "error", err)
	}
	h.publishKnowledge(wsUUID, r, docUUID, "removed")
	w.WriteHeader(http.StatusNoContent)
}

// ReprocessKnowledge — POST /api/knowledge/{id}/reprocess: read the file
// again (after a reader fix, or a failed attempt).
func (h *Handler) ReprocessKnowledge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := h.knowledgeWorkspace(w, r)
	if !ok || !h.requireKnowledgeManager(w, r, wsUUID) {
		return
	}
	docUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "document id")
	if !ok {
		return
	}
	doc, err := h.Queries.RequeueKnowledgeDoc(r.Context(), db.RequeueKnowledgeDocParams{ID: docUUID, WorkspaceID: wsUUID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reprocess the document")
		return
	}
	h.publishKnowledge(wsUUID, r, doc.ID, doc.Status)
	h.enqueueKnowledgeIngest(doc.ID)
	writeJSON(w, http.StatusOK, knowledgeDocFromRow(doc, ""))
}

type knowledgeSearchResult struct {
	ChunkID     string `json:"chunk_id"`
	DocID       string `json:"doc_id"`
	DocTitle    string `json:"doc_title"`
	Section     int    `json:"section"`
	HeadingPath string `json:"heading_path"`
	Location    string `json:"location"`
	Snippet     string `json:"snippet"`
	Cite        string `json:"cite"`
}

// SearchKnowledge — GET /api/knowledge/search?q=&limit=.
func (h *Handler) SearchKnowledge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := h.knowledgeWorkspace(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	hits, err := h.searchKnowledge(r.Context(), wsUUID, q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	if q != "" {
		userUUID, _ := knowledgeUUID(requestUserID(r))
		h.logKnowledgeSearch(r.Context(), wsUUID, userUUID, "ui", q, hits)
	}
	out := make([]knowledgeSearchResult, 0, len(hits))
	for _, hit := range hits {
		out = append(out, knowledgeSearchResult{
			ChunkID: hit.ChunkID, DocID: hit.DocID, DocTitle: hit.DocTitle, Section: hit.Section,
			HeadingPath: hit.HeadingPath, Location: hit.Location, Snippet: clipRunes(strings.TrimSpace(hit.Text), 280), Cite: hit.Cite,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}
