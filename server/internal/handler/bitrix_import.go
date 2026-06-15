package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Bitrix → Agora enrichment: a Bitrix workgroup becomes an Agora project, a
// task's comment feed becomes issue comments, and its attachments become issue
// attachments (with video recordings decomposed into still frames so the
// Planner's claim brief can "see" the bug). All of this runs in the detached
// sync goroutine the webhook already spawns; every step is bounded and
// best-effort so a slow/missing portal scope never blocks task import.

// --- group → project --------------------------------------------------------

// getOrCreateBitrixProject returns the Agora project id for a Bitrix workgroup
// in the given workspace, creating it on first sight. Dedup is durable: the
// project's description carries a "bitrix_group:<id>" marker, and an existing
// project is found by querying that marker (raw pgx — no new sqlc method, so the
// change stays surgical + upstream-mergeable). Resolutions are cached on st for
// the duration of a batch import.
func (h *Handler) getOrCreateBitrixProject(ctx context.Context, workspaceID pgtype.UUID, groupID, groupName string, st *bitrixSyncState) (pgtype.UUID, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return pgtype.UUID{}, fmt.Errorf("empty group id")
	}
	cacheKey := util.UUIDToString(workspaceID) + ":" + groupID
	if id, ok := st.projectCache[cacheKey]; ok {
		return id, nil
	}

	marker := bitrixProjectMarkerPrefix + groupID

	// Look up an existing project for this group via the durable description
	// marker. metadata-free: project has no JSONB column, so the marker lives in
	// the description text. Scoped to the workspace (tenant boundary).
	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project
		  WHERE workspace_id = $1 AND description LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		workspaceID, marker).Scan(&existingID)
	if err == nil {
		st.projectCache[cacheKey] = existingID
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, fmt.Errorf("lookup bitrix project: %w", err)
	}

	// Create it. Title is the group name (fall back to a stable placeholder so a
	// nameless group still files). Description carries the marker on its own line
	// so the LIKE dedup is reliable and a human reading the project sees the link.
	title := strings.TrimSpace(groupName)
	if title == "" {
		title = "Bitrix group " + groupID
	}
	description := "Imported from Bitrix workgroup.\n" + marker

	project, err := h.Queries.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: workspaceID,
		Title:       title,
		Description: strToText(description),
		Status:      "planned", // matches CreateProject handler default
		Priority:    "none",    // matches CreateProject handler default
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create bitrix project: %w", err)
	}
	st.projectCache[cacheKey] = project.ID
	slog.Info("bitrix sync: created project for workgroup",
		"project_id", util.UUIDToString(project.ID),
		"group_id", groupID, "title", title,
		"workspace_id", util.UUIDToString(workspaceID))
	return project.ID, nil
}

// --- comments ---------------------------------------------------------------

// importBitrixComments mirrors a task's Bitrix comment feed onto the freshly
// created issue as issue comments, once. Each Bitrix comment becomes one
// member-authored issue comment (author = workspace owner, since the
// integration has no member of its own) with a clear provenance header.
// Bounded by the client (maxCommentsPerTask) and idempotent via the
// bitrix_comments_imported metadata flag. All failures are logged, never fatal.
func (h *Handler) importBitrixComments(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, taskID string, st *bitrixSyncState) {
	comments, err := st.client.GetTaskComments(ctx, taskID)
	if err != nil {
		slog.Warn("bitrix sync: fetch comments failed", "task_id", taskID, "error", err)
		return
	}
	if len(comments) == 0 {
		return
	}

	imported := 0
	for _, c := range comments {
		content := formatBitrixComment(c)
		if strings.TrimSpace(content) == "" {
			continue
		}
		if _, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID:     issueID,
			WorkspaceID: wsID,
			AuthorType:  "member",
			AuthorID:    ownerID,
			Content:     content,
			Type:        "comment",
		}); err != nil {
			slog.Warn("bitrix sync: create comment failed",
				"task_id", taskID, "issue_id", util.UUIDToString(issueID), "error", err)
			continue
		}
		imported++
	}

	// Mark done so a re-sync (ONTASKUPDATE) doesn't duplicate the feed.
	h.setBitrixImportFlag(ctx, wsID, issueID, bitrixCommentsImportedMetaKey)
	slog.Info("bitrix sync: imported comments",
		"task_id", taskID, "issue_id", util.UUIDToString(issueID), "count", imported)
}

// formatBitrixComment renders a Bitrix comment as an Agora issue-comment body
// with a provenance header: "**Bitrix — <author> (<date>)**:\n<text>". A
// missing author/date degrades gracefully.
func formatBitrixComment(c bitrix.Comment) string {
	author := strings.TrimSpace(c.Author)
	if author == "" {
		author = "unknown"
	}
	header := "**Bitrix — " + author
	if d := strings.TrimSpace(c.Date); d != "" {
		header += " (" + d + ")"
	}
	header += "**:"
	return header + "\n" + bitrix.BBCodeToMarkdown(strings.TrimSpace(c.Text))
}

// --- attachments + video frames ---------------------------------------------

// maxBitrixVideoFrames caps how many extracted frames are uploaded per video.
const maxBitrixVideoFrames = bitrix.MaxVideoFrames

// bitrixFrameExtractTimeout bounds a single ffmpeg/ffprobe invocation so a
// pathological video can't wedge the sync goroutine.
const bitrixFrameExtractTimeout = 60 * time.Second

// importBitrixAttachments downloads a task's attachments and stores them as
// issue attachments, once. Video files are additionally decomposed into still
// frames (uploaded as image attachments) so an agent that can't watch a
// recording still gets the key states. Bounded by the client
// (maxFilesPerTask) and idempotent via the bitrix_files_imported metadata flag.
// Requires Storage; a no-op (logged) when storage is unconfigured. All failures
// are logged, never fatal.
func (h *Handler) importBitrixAttachments(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, taskID string, st *bitrixSyncState) {
	if h.Storage == nil {
		slog.Debug("bitrix sync: storage not configured, skipping attachments", "task_id", taskID)
		return
	}
	files, err := st.client.GetTaskFiles(ctx, taskID)
	if err != nil {
		slog.Warn("bitrix sync: fetch files failed", "task_id", taskID, "error", err)
		return
	}
	if len(files) == 0 {
		return
	}

	stored := 0
	frames := 0
	var embeds []bitrixEmbed
	for _, f := range files {
		data, ctype, err := st.client.DownloadFile(ctx, f.URL)
		if err != nil {
			slog.Warn("bitrix sync: download attachment failed",
				"task_id", taskID, "name", f.Name, "error", err)
			continue
		}
		contentType := pickAttachmentContentType(ctype, f.Name)
		_, url, err := h.storeBitrixAttachment(ctx, wsID, issueID, ownerID, f.Name, contentType, data)
		if err != nil {
			slog.Warn("bitrix sync: store attachment failed",
				"task_id", taskID, "name", f.Name, "error", err)
			continue
		}
		stored++
		embeds = append(embeds, bitrixEmbed{url: url, name: f.Name, contentType: contentType})

		// Video → frames. Best-effort: if ffmpeg is missing or extraction
		// fails, the original video attachment is still stored above.
		if bitrix.IsVideo(f.Name, contentType) {
			frameEmbeds := h.extractAndStoreFrames(ctx, wsID, issueID, ownerID, f.Name, data, st)
			frames += len(frameEmbeds)
			embeds = append(embeds, frameEmbeds...)
		}
	}

	// Surface every stored file/frame inline in the issue description.
	h.appendBitrixAttachmentsToDescription(ctx, wsID, issueID, embeds)

	h.setBitrixImportFlag(ctx, wsID, issueID, bitrixFilesImportedMetaKey)
	slog.Info("bitrix sync: imported attachments",
		"task_id", taskID, "issue_id", util.UUIDToString(issueID),
		"files", stored, "frames", frames)
}

// storeBitrixAttachment uploads bytes to Storage and records the attachment row
// linked to the issue, mirroring the /api/upload-file path (storage.Upload +
// CreateAttachment). The uploader is the workspace owner ("member"). Returns the
// created attachment id and its public URL (for embedding in the description).
func (h *Handler) storeBitrixAttachment(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, filename, contentType string, data []byte) (pgtype.UUID, string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("generate attachment id: %w", err)
	}
	// Same key layout as UploadFile: workspaces/<ws>/<uuid><ext>.
	key := "workspaces/" + util.UUIDToString(wsID) + "/" + id.String() + path.Ext(filename)
	link, err := h.Storage.Upload(ctx, key, data, contentType, filename)
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("upload: %w", err)
	}
	att, err := h.Queries.CreateAttachment(ctx, db.CreateAttachmentParams{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		WorkspaceID:  wsID,
		UploaderType: "member",
		UploaderID:   ownerID,
		Filename:     filename,
		Url:          link,
		ContentType:  contentType,
		SizeBytes:    int64(len(data)),
		IssueID:      issueID,
	})
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("create attachment row: %w", err)
	}
	return att.ID, link, nil
}

// bitrixEmbed is one stored attachment (file or extracted frame) to surface in
// the issue description as inline markdown.
type bitrixEmbed struct {
	url         string
	name        string
	contentType string
}

// appendBitrixAttachmentsToDescription appends a markdown block linking every
// imported attachment to the issue description, so the issue view AND the
// Planner's claim brief see the screenshots/frames inline instead of as orphan
// attachment rows. Images embed (![]); videos and other files render as links.
// Best-effort: a failure is logged, never fatal (the attachment rows already
// exist). Runs once, on first import (the same create-only path as the rows).
func (h *Handler) appendBitrixAttachmentsToDescription(ctx context.Context, wsID, issueID pgtype.UUID, embeds []bitrixEmbed) {
	block := bitrixAttachmentBlock(embeds)
	if block == "" {
		return
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE issue SET description = coalesce(description, '') || $3, updated_at = now()
		   WHERE id = $1 AND workspace_id = $2`,
		issueID, wsID, block); err != nil {
		slog.Warn("bitrix sync: append attachments to description failed",
			"issue_id", util.UUIDToString(issueID), "error", err)
	}
}

// bitrixAttachmentBlock renders the markdown block appended to an issue
// description for the imported attachments. Images embed inline (![]); videos
// and other files render as labelled links. Returns "" when there is nothing to
// embed.
func bitrixAttachmentBlock(embeds []bitrixEmbed) string {
	if len(embeds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n---\n\n**Attachments (from Bitrix):**\n\n")
	for _, e := range embeds {
		name := sanitizeEmbedName(e.name)
		switch {
		case strings.HasPrefix(e.contentType, "image/"):
			fmt.Fprintf(&b, "![%s](%s)\n\n", name, e.url)
		case strings.HasPrefix(e.contentType, "video/"):
			fmt.Fprintf(&b, "🎬 [%s](%s)\n\n", name, e.url)
		default:
			fmt.Fprintf(&b, "📎 [%s](%s)\n\n", name, e.url)
		}
	}
	return b.String()
}

// sanitizeEmbedName makes a filename safe for a markdown link/alt label: brackets
// would terminate the [..](..) syntax and newlines would break the block.
func sanitizeEmbedName(name string) string {
	return strings.TrimSpace(strings.NewReplacer(
		"[", "(", "]", ")", "\n", " ", "\r", " ",
	).Replace(name))
}

// extractAndStoreFrames writes the video bytes to a temp file, runs ffmpeg to
// pull still frames (scene detection with an interval fallback, mirroring the
// legacy bot), and uploads each frame as an image attachment on the issue.
// Returns the stored frames as embeds (for inline description embedding); its
// length is the count. ffmpeg missing / any failure logs and returns nil — never
// fatal.
func (h *Handler) extractAndStoreFrames(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, filename string, data []byte, st *bitrixSyncState) []bitrixEmbed {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("bitrix sync: ffmpeg not found, skipping video frames", "filename", filename)
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "bitrix-frames-*")
	if err != nil {
		slog.Warn("bitrix sync: temp dir failed", "error", err)
		return nil
	}
	defer os.RemoveAll(tmpDir)

	ext := path.Ext(filename)
	if ext == "" {
		ext = ".mp4"
	}
	srcPath := filepath.Join(tmpDir, "source"+ext)
	if err := os.WriteFile(srcPath, data, 0o600); err != nil {
		slog.Warn("bitrix sync: write temp video failed", "error", err)
		return nil
	}

	framePaths := extractVideoFrames(ctx, srcPath, tmpDir)
	if len(framePaths) == 0 {
		slog.Debug("bitrix sync: no frames extracted", "filename", filename)
		return nil
	}

	base := strings.TrimSuffix(path.Base(filename), ext)
	var embeds []bitrixEmbed
	for i, fp := range framePaths {
		if len(embeds) >= maxBitrixVideoFrames {
			break
		}
		frameBytes, err := os.ReadFile(fp)
		if err != nil || len(frameBytes) == 0 {
			continue
		}
		frameName := fmt.Sprintf("%s_frame_%03d.jpg", base, i+1)
		_, url, err := h.storeBitrixAttachment(ctx, wsID, issueID, ownerID, frameName, "image/jpeg", frameBytes)
		if err != nil {
			slog.Warn("bitrix sync: store frame failed", "name", frameName, "error", err)
			continue
		}
		embeds = append(embeds, bitrixEmbed{url: url, name: frameName, contentType: "image/jpeg"})
	}
	return embeds
}

// extractVideoFrames shells out to ffmpeg to pull stills from srcPath into
// outDir, returning the sorted frame paths. It runs the scene-detection pass
// first and, when that yields too few frames for a non-trivial video, an
// interval-sampling fallback — the exact two-stage strategy of the legacy bot.
// The ffmpeg argument vectors come from the pure bitrix.*Args builders so the
// command shape is unit-testable without ffmpeg present.
func extractVideoFrames(ctx context.Context, srcPath, outDir string) []string {
	duration := probeVideoDuration(ctx, srcPath)

	// Primary: scene-change detection.
	scenePattern := filepath.Join(outDir, "scene_%03d.jpg")
	runFFmpeg(ctx, bitrix.SceneDetectArgs(srcPath, scenePattern, maxBitrixVideoFrames, bitrix.DefaultSceneThreshold))
	frames := globSorted(outDir, "scene_")

	// Fallback: interval sampling when scene detection found too few cuts.
	if bitrix.NeedsIntervalFallback(len(frames), duration) {
		n := bitrix.IntervalFrameCount(duration, maxBitrixVideoFrames)
		for i, ts := range bitrix.IntervalTimestamps(duration, n) {
			out := filepath.Join(outDir, fmt.Sprintf("interval_%03d.jpg", i))
			runFFmpeg(ctx, bitrix.IntervalFrameArgs(srcPath, out, ts))
		}
		// Re-glob both prefixes so scene + interval frames are all returned.
		frames = append(globSorted(outDir, "scene_"), globSorted(outDir, "interval_")...)
	}
	if len(frames) > maxBitrixVideoFrames {
		frames = frames[:maxBitrixVideoFrames]
	}
	return frames
}

// probeVideoDuration returns a video's duration in seconds via ffprobe, or 0 on
// any failure (ffprobe missing, unparseable output). A 0 duration disables the
// interval fallback's coverage math, which is the safe degrade.
func probeVideoDuration(ctx context.Context, srcPath string) float64 {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0
	}
	cctx, cancel := context.WithTimeout(ctx, bitrixFrameExtractTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ffprobe", bitrix.ProbeDurationArgs(srcPath)...).Output()
	if err != nil {
		return 0
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return d
}

// runFFmpeg runs one bounded ffmpeg invocation, discarding output. Errors are
// swallowed — the caller inspects the produced files, not the exit code, and a
// partial run (some frames written) is still useful.
func runFFmpeg(ctx context.Context, args []string) {
	cctx, cancel := context.WithTimeout(ctx, bitrixFrameExtractTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ffmpeg", args...)
	if err := cmd.Run(); err != nil {
		slog.Debug("bitrix sync: ffmpeg run returned error (continuing)", "error", err)
	}
}

// globSorted returns the lexically-sorted files in dir whose base name starts
// with prefix. ffmpeg writes zero-padded indices, so lexical order == frame
// order.
func globSorted(dir, prefix string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, prefix+"*"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

// --- shared helpers ---------------------------------------------------------

// pickAttachmentContentType resolves a sane content type for a downloaded
// attachment: the server-reported type wins when it is concrete (not the
// generic octet-stream), otherwise it is inferred from the filename extension,
// falling back to application/octet-stream.
func pickAttachmentContentType(reported, filename string) string {
	reported = strings.TrimSpace(reported)
	// Strip any "; charset=..." parameter.
	if i := strings.Index(reported, ";"); i >= 0 {
		reported = strings.TrimSpace(reported[:i])
	}
	if reported != "" && reported != "application/octet-stream" {
		return reported
	}
	if byExt := mime.TypeByExtension(strings.ToLower(path.Ext(filename))); byExt != "" {
		if i := strings.Index(byExt, ";"); i >= 0 {
			byExt = strings.TrimSpace(byExt[:i])
		}
		return byExt
	}
	if reported != "" {
		return reported
	}
	return "application/octet-stream"
}

// setBitrixImportFlag stamps a boolean true under the given metadata key on the
// issue, marking a one-time import (comments / files) as done. Failures are
// logged, not fatal — at worst a re-sync re-imports, which the create-only
// guard already prevents by returning before this path on the dedup branch.
func (h *Handler) setBitrixImportFlag(ctx context.Context, wsID, issueID pgtype.UUID, key string) {
	val, _ := json.Marshal(true)
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issueID,
		WorkspaceID: wsID,
		Key:         key,
		Value:       val,
	}); err != nil {
		slog.Debug("bitrix sync: set import flag failed", "key", key, "error", err)
	}
}
