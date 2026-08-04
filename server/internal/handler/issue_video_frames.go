package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
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
	// loadIssueForUser resolves {id} (UUID or MUL-N) AND gates it to a workspace
	// the caller is a member of. Without it this fired a background mutation
	// (ffmpeg → issue description + attachments) on ANY issue by raw UUID across
	// tenants, and doubled as a 202/404 existence oracle for arbitrary ids (F6).
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	h.kickVideoFrameExtraction(issue)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "started"})
}

// frameExtractLocks serializes frame extraction per issue so the on-assign
// background kick and the claim-time synchronous pass never run ffmpeg on the
// same video concurrently — two runs would each append the same frames to the
// description before either sets the idempotency flag. Keyed by issue UUID.
var frameExtractLocks sync.Map

// lockIssueFrames blocks until it holds the issue's extraction lock. Used by the
// background kick, which has a generous budget and must not drop work.
func lockIssueFrames(issueID pgtype.UUID) func() {
	muAny, _ := frameExtractLocks.LoadOrStore(util.UUIDToString(issueID), &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// tryLockIssueFrames acquires the issue's extraction lock without blocking. Used
// by the claim path: if another extraction is already in flight we must NOT
// block the claim (its HTTP budget is 30s), so we skip and let the in-flight run
// finish — the plan for this claim just proceeds on whatever description exists.
func tryLockIssueFrames(issueID pgtype.UUID) (func(), bool) {
	muAny, _ := frameExtractLocks.LoadOrStore(util.UUIDToString(issueID), &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	if !mu.TryLock() {
		return func() {}, false
	}
	return mu.Unlock, true
}

// claimFrameExtractTimeout bounds the claim-time synchronous extraction. It must
// stay well under the daemon's 30s claim HTTP timeout (internal/daemon/client.go)
// so a slow ffmpeg pass can't make the whole claim time out; a recording that
// can't be decoded in this window falls back to the background kick / next claim.
const claimFrameExtractTimeout = 18 * time.Second

// kickVideoFrameExtraction runs extraction in the background on a detached
// context, so callers (the assign/planning path, or the endpoint) never block on
// ffmpeg. Self-guarding: no Storage, no workspace owner, no video attachments, or
// already-extracted all short-circuit to a no-op.
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
		unlock := lockIssueFrames(issueID) // blocking — the kick must not drop work
		defer unlock()
		h.extractFramesForIssueLocked(ctx, wsID, issueID, owner)
	}()
}

// ensureVideoFramesForBrief runs extraction SYNCHRONOUSLY (bounded by ctx) when
// the issue still has un-extracted videos, then returns the issue re-loaded so
// its Description carries the freshly appended frame images. Called from the
// claim path immediately before the plan brief is built, so a video-heavy ticket
// reaches the agent's plan as stills instead of racing the async on-assign kick.
// Fast no-op when frames are already extracted. Non-blocking on the per-issue
// lock: if the background kick is mid-extraction we skip rather than stall the
// claim — the description just lacks frames for this one claim (no regression).
func (h *Handler) ensureVideoFramesForBrief(ctx context.Context, issue db.Issue) db.Issue {
	if h.Storage == nil || !issue.ID.Valid {
		return issue
	}
	if metadataFlagSet(issue.Metadata, bitrixFramesExtractedMetaKey) {
		return issue
	}
	owner, err := h.bitrixWorkspaceOwner(ctx, issue.WorkspaceID)
	if err != nil {
		return issue
	}
	unlock, ok := tryLockIssueFrames(issue.ID)
	if !ok {
		return issue // another extraction in flight — don't block the claim
	}
	defer unlock()
	h.extractFramesForIssueLocked(ctx, issue.WorkspaceID, issue.ID, owner)
	if fresh, ferr := h.Queries.GetIssue(ctx, issue.ID); ferr == nil {
		return fresh
	}
	return issue
}

// extractFramesForIssueLocked reads the issue's stored video attachments and, for
// each, runs ffmpeg to extract still frames (stored as image attachments and
// appended to the description). Idempotent via the bitrix_frames_extracted
// metadata flag. Best-effort: every failure is logged, never fatal.
//
// The caller MUST hold the issue's frame lock (lockIssueFrames /
// tryLockIssueFrames): the flag re-check below is the dedup, and it is only sound
// when serialized against a concurrent extraction of the same issue.
func (h *Handler) extractFramesForIssueLocked(ctx context.Context, wsID, issueID, ownerID pgtype.UUID) {
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
