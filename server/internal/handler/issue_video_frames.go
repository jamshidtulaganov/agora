package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// bitrixFramesExtractedMetaKey marks an issue whose stored video attachments
// have already been decomposed into still frames, so re-runs are no-ops.
const bitrixFramesExtractedMetaKey = "bitrix_frames_extracted"

// frameExtractTimeout bounds a background frame-extraction run (video downloads
// from storage + ffmpeg). Generous because a screen-recording can be large.
const frameExtractTimeout = 15 * time.Minute

// ExtractIssueVideoFrames kicks a background ffmpeg pass over the issue's stored
// video attachments, decomposing each into still frames (image attachments +
// inline in the description) so an agent that can't watch a recording still
// sees the key states. Returns 202 immediately; the work runs detached. This is
// the planning-time counterpart to the import, which no longer extracts frames
// (it only downloads the video) — see importBitrixAttachments.
func (h *Handler) ExtractIssueVideoFrames(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), id)
	if err != nil || !issue.ID.Valid {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	h.kickVideoFrameExtraction(issue)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "started"})
}

// kickVideoFrameExtraction runs extractFramesForIssue in the background on a
// detached context, so callers (the assign/planning path, or the endpoint) never
// block on ffmpeg. Self-guarding: no Storage, no workspace owner, no video
// attachments, or already-extracted all short-circuit to a no-op.
func (h *Handler) kickVideoFrameExtraction(issue db.Issue) {
	if h.Storage == nil || !issue.ID.Valid {
		return
	}
	owner, err := h.bitrixWorkspaceOwner(context.Background(), issue.WorkspaceID)
	if err != nil {
		slog.Warn("video frames: no workspace owner, skipping",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	wsID, issueID := issue.WorkspaceID, issue.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), frameExtractTimeout)
		defer cancel()
		h.extractFramesForIssue(ctx, wsID, issueID, owner)
	}()
}

// extractFramesForIssue reads the issue's stored video attachments and, for
// each, runs ffmpeg to extract still frames (stored as image attachments and
// appended to the description). Idempotent via the bitrix_frames_extracted
// metadata flag. Best-effort: every failure is logged, never fatal.
func (h *Handler) extractFramesForIssue(ctx context.Context, wsID, issueID, ownerID pgtype.UUID) {
	if h.Storage == nil {
		return
	}
	// Idempotency: skip when frames were already extracted for this issue.
	if issue, err := h.Queries.GetIssue(ctx, issueID); err == nil &&
		metadataFlagSet(issue.Metadata, bitrixFramesExtractedMetaKey) {
		return
	}

	atts, err := h.Queries.ListAttachmentsByIssue(ctx, db.ListAttachmentsByIssueParams{
		IssueID:     issueID,
		WorkspaceID: wsID,
	})
	if err != nil {
		slog.Warn("video frames: list attachments failed",
			"issue_id", util.UUIDToString(issueID), "error", err)
		return
	}

	var embeds []bitrixEmbed
	videos := 0
	for _, a := range atts {
		if !bitrix.IsVideo(a.Filename, a.ContentType) {
			continue
		}
		videos++
		key := h.Storage.KeyFromURL(a.Url)
		rc, err := h.Storage.GetReader(ctx, key)
		if err != nil {
			slog.Warn("video frames: read stored video failed",
				"issue_id", util.UUIDToString(issueID), "name", a.Filename, "error", err)
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || len(data) == 0 {
			continue
		}
		// st is unused by extractAndStoreFrames; nil is safe.
		embeds = append(embeds, h.extractAndStoreFrames(ctx, wsID, issueID, ownerID, a.Filename, data, nil)...)
	}

	if videos == 0 {
		return // no videos yet — don't set the flag so a later upload still extracts
	}
	if len(embeds) > 0 {
		h.appendBitrixAttachmentsToDescription(ctx, wsID, issueID, embeds)
	}
	h.setBitrixImportFlag(ctx, wsID, issueID, bitrixFramesExtractedMetaKey)
	slog.Info("video frames extracted",
		"issue_id", util.UUIDToString(issueID), "videos", videos, "frames", len(embeds))
}

// metadataFlagSet reports whether a JSONB metadata blob has key set to a truthy
// value (true, or a non-empty string).
func metadataFlagSet(metadata []byte, key string) bool {
	if len(metadata) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(metadata, &m); err != nil {
		return false
	}
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false"
	default:
		return v != nil
	}
}
