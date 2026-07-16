package handler

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const artifactCapabilityTTL = 30 * time.Minute

type artifactRepoRef struct {
	Repo        string `json:"repo"`
	Branch      string `json:"branch,omitempty"`
	BaseSHA     string `json:"base_sha"`
	HeadSHA     string `json:"head_sha"`
	MergeStatus string `json:"merge_status"`
}

type artifactSummary struct {
	ID          string            `json:"id"`
	RunID       string            `json:"run_id"`
	StepID      string            `json:"step_id"`
	StepKey     string            `json:"step_key"`
	Title       string            `json:"title"`
	Kind        string            `json:"kind"`
	Capability  string            `json:"capability"`
	Canonical   bool              `json:"canonical"`
	Repos       []artifactRepoRef `json:"repos"`
	CompletedAt any               `json:"completed_at,omitempty"`
}

type issueArtifactResponse struct {
	RunID        string            `json:"run_id"`
	RunStatus    string            `json:"run_status"`
	Ready        bool              `json:"ready"`
	Reason       string            `json:"reason,omitempty"`
	Artifact     *artifactSummary  `json:"artifact,omitempty"`
	Components   []artifactSummary `json:"components"`
	DaemonURL    string            `json:"daemon_url,omitempty"`
	Capabilities map[string]string `json:"capabilities,omitempty"`
}

type artifactCapabilityRecord struct {
	ID          string            `json:"id"`
	ArtifactID  string            `json:"artifact_id"`
	Purpose     string            `json:"purpose"`
	WorkspaceID string            `json:"workspace_id"`
	IssueID     string            `json:"issue_id"`
	RunID       string            `json:"run_id"`
	StepID      string            `json:"step_id"`
	RuntimeID   string            `json:"runtime_id,omitempty"`
	DaemonID    string            `json:"daemon_id,omitempty"`
	SourceRoot  string            `json:"source_root"`
	Repos       []artifactRepoRef `json:"repos"`
	// Live marks a grant that points at a local_directory's LIVE working tree
	// (no orchestration step, no frozen SHAs). The daemon reads the folder's
	// uncommitted working state for changes/file and does not require repo
	// base/head SHAs. PreviewURL is the developer's own running dev server that
	// the preview surface proxies to instead of spawning one.
	Live       bool      `json:"live,omitempty"`
	PreviewURL string    `json:"preview_url,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

const artifactCapabilityAAD = "agora-artifact-capability-v1"

func artifactCapabilityKey() [sha256.Size]byte {
	material := append([]byte(artifactCapabilityAAD+"\x00"), auth.JWTSecret()...)
	return sha256.Sum256(material)
}

func sealArtifactCapability(record artifactCapabilityRecord) (string, error) {
	if record.ID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", err
		}
		record.ID = hex.EncodeToString(id[:])
	}
	plaintext, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	key := artifactCapabilityKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, []byte(artifactCapabilityAAD))
	return "acp_" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func mintArtifactCapability(record artifactCapabilityRecord) (string, error) {
	record.ExpiresAt = time.Now().Add(artifactCapabilityTTL)
	return sealArtifactCapability(record)
}

func lookupArtifactCapability(token string) (artifactCapabilityRecord, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "acp_") {
		return artifactCapabilityRecord{}, false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "acp_"))
	if err != nil {
		return artifactCapabilityRecord{}, false
	}
	key := artifactCapabilityKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return artifactCapabilityRecord{}, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return artifactCapabilityRecord{}, false
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(artifactCapabilityAAD))
	if err != nil {
		return artifactCapabilityRecord{}, false
	}
	var record artifactCapabilityRecord
	if json.Unmarshal(plaintext, &record) != nil || record.ID == "" || record.ArtifactID == "" || record.Purpose == "" || time.Now().After(record.ExpiresAt) {
		return artifactCapabilityRecord{}, false
	}
	return record, true
}

func decodeArtifactRepos(step db.OrchestrationStep) []artifactRepoRef {
	var states []RepoGitStateResponse
	_ = json.Unmarshal(step.GitStates, &states)
	if len(states) == 0 && strings.TrimSpace(step.BaseSha.String) != "" && strings.TrimSpace(step.HeadSha.String) != "" {
		states = []RepoGitStateResponse{{
			Repo: "repository", Branch: step.WorktreeBranch.String,
			BaseSHA: step.BaseSha.String, HeadSHA: step.HeadSha.String, MergeStatus: step.MergeStatus,
		}}
	}
	refs := make([]artifactRepoRef, 0, len(states))
	seen := make(map[string]bool, len(states))
	for _, state := range states {
		ref := artifactRepoRef{
			Repo: strings.TrimSpace(state.Repo), Branch: strings.TrimSpace(state.Branch),
			BaseSHA:     strings.ToLower(strings.TrimSpace(state.BaseSHA)),
			HeadSHA:     strings.ToLower(strings.TrimSpace(state.HeadSHA)),
			MergeStatus: strings.TrimSpace(state.MergeStatus),
		}
		if ref.Repo == "" || seen[ref.Repo] || !validFullGitCommitSHA(ref.BaseSHA) || !validFullGitCommitSHA(ref.HeadSHA) || ref.MergeStatus != "clean" {
			return nil
		}
		seen[ref.Repo] = true
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Repo < refs[j].Repo })
	return refs
}

func artifactID(runID, stepID string, refs []artifactRepoRef) string {
	h := sha256.New()
	_, _ = h.Write([]byte(runID + "\x00" + stepID))
	for _, ref := range refs {
		_, _ = h.Write([]byte("\x00" + ref.Repo + "\x00" + ref.BaseSHA + "\x00" + ref.HeadSHA))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func artifactSummaryForStep(runID string, step db.OrchestrationStep, canonical bool) (artifactSummary, bool) {
	if step.Stage != "dev" || step.Status != "completed" || step.MergeStatus != "clean" {
		return artifactSummary{}, false
	}
	if step.StepKind == "integration" && step.IntegrationStatus != "complete" {
		return artifactSummary{}, false
	}
	refs := decodeArtifactRepos(step)
	if len(refs) == 0 {
		return artifactSummary{}, false
	}
	summary := artifactSummary{
		RunID: runID, StepID: uuidToString(step.ID), StepKey: step.StepKey,
		Title: step.Title, Kind: step.StepKind, Capability: step.Capability,
		Canonical: canonical, Repos: refs,
	}
	summary.ID = artifactID(runID, summary.StepID, refs)
	if step.CompletedAt.Valid {
		summary.CompletedAt = step.CompletedAt.Time
	}
	return summary, true
}

// selectCanonicalArtifact prefers a proven integration result. A plan that has
// an integration step never falls back to an individual worker branch while the
// join is pending. Solo/sequential plans without a join use their last completed
// development step.
func selectCanonicalArtifact(runID string, steps []db.OrchestrationStep) (artifactSummary, bool) {
	hasIntegration := false
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if step.StepKind != "integration" {
			continue
		}
		hasIntegration = true
		if summary, ok := artifactSummaryForStep(runID, step, true); ok {
			return summary, true
		}
	}
	if hasIntegration {
		return artifactSummary{}, false
	}
	for index := len(steps) - 1; index >= 0; index-- {
		if summary, ok := artifactSummaryForStep(runID, steps[index], true); ok {
			return summary, true
		}
	}
	return artifactSummary{}, false
}

func artifactComponents(runID string, steps []db.OrchestrationStep, canonicalID string) []artifactSummary {
	result := make([]artifactSummary, 0, len(steps))
	for _, step := range steps {
		summary, ok := artifactSummaryForStep(runID, step, false)
		if !ok {
			continue
		}
		summary.Canonical = summary.ID == canonicalID
		result = append(result, summary)
	}
	return result
}

type artifactRuntimeLocation struct {
	WorkDir    string
	RuntimeID  string
	DaemonID   string
	EditorAddr string
	EditorPort string
}

func (h *Handler) artifactRuntimeLocation(ctx context.Context, taskID pgtype.UUID) (artifactRuntimeLocation, error) {
	var location artifactRuntimeLocation
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(atq.work_dir, ''), COALESCE(atq.runtime_id::text, ''),
		       COALESCE(ar.daemon_id, ''), COALESCE(ar.metadata->>'editor_addr', ''),
		       COALESCE(ar.metadata->>'editor_port', '')
		FROM agent_task_queue atq
		LEFT JOIN agent_runtime ar ON ar.id = atq.runtime_id
		WHERE atq.id = $1
	`, taskID).Scan(&location.WorkDir, &location.RuntimeID, &location.DaemonID, &location.EditorAddr, &location.EditorPort)
	return location, err
}

// GetIssueArtifact returns immutable Git identity plus short-lived opaque
// capabilities. Raw filesystem paths never cross this API boundary.
// liveLocalArtifactID is deterministic per (issue, folder) so the frontend's
// query cache keys stay stable across polls of a live local working tree.
func liveLocalArtifactID(issueID, localPath string) string {
	h := sha256.Sum256([]byte("live\x00" + issueID + "\x00" + localPath))
	return "live-" + hex.EncodeToString(h[:8])
}

// writeLiveLocalArtifact serves the issue's project local_directory as a LIVE
// artifact — its working tree, no orchestration step — when one is attached on
// an online daemon. Returns true when it wrote a response. This is what makes
// Changes/Preview/Checks reflect the folder's current state (and proxy to the
// developer's own dev server) even with no orchestration run.
func (h *Handler) writeLiveLocalArtifact(w http.ResponseWriter, r *http.Request, issue db.Issue) bool {
	if !issue.ProjectID.Valid {
		return false
	}
	resources, err := h.Queries.ListProjectResources(r.Context(), issue.ProjectID)
	if err != nil {
		return false
	}
	var ref localDirectoryRef
	found := false
	for _, res := range resources {
		if res.ResourceType != "local_directory" {
			continue
		}
		if json.Unmarshal(res.ResourceRef, &ref) != nil {
			continue
		}
		if strings.TrimSpace(ref.LocalPath) != "" && strings.TrimSpace(ref.DaemonID) != "" {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	runtime, err := h.Queries.GetOnlineRuntimeForDaemon(r.Context(), db.GetOnlineRuntimeForDaemonParams{
		WorkspaceID: issue.WorkspaceID, DaemonID: pgtype.Text{String: ref.DaemonID, Valid: true},
	})
	if err != nil {
		return false // daemon offline — no live surface, fall through
	}
	var meta struct {
		EditorAddr string `json:"editor_addr"`
		EditorPort string `json:"editor_port"`
	}
	_ = json.Unmarshal(runtime.Metadata, &meta)

	artifactID := liveLocalArtifactID(uuidToString(issue.ID), ref.LocalPath)
	summary := artifactSummary{
		ID: artifactID, StepKey: "local", Title: "Local working tree",
		Kind: "local_directory", Capability: "implementation", Canonical: true,
		Repos: []artifactRepoRef{},
	}
	baseRecord := artifactCapabilityRecord{
		ArtifactID: artifactID, WorkspaceID: uuidToString(issue.WorkspaceID),
		IssueID: uuidToString(issue.ID), RuntimeID: uuidToString(runtime.ID), DaemonID: ref.DaemonID,
		SourceRoot: ref.LocalPath, Live: true, PreviewURL: strings.TrimSpace(ref.PreviewURL),
	}
	capabilities := make(map[string]string, 4)
	for _, purpose := range []string{"changes", "file", "preview", "checks"} {
		record := baseRecord
		record.Purpose = purpose
		token, mintErr := mintArtifactCapability(record)
		if mintErr != nil {
			writeError(w, http.StatusInternalServerError, "mint artifact capability failed")
			return true
		}
		capabilities[purpose] = token
	}
	response := issueArtifactResponse{
		Ready: true, RunStatus: "live", Artifact: &summary,
		Components: []artifactSummary{summary}, Capabilities: capabilities,
	}
	if internal := resolveDaemonInternalAddr(meta.EditorAddr); internal != "" {
		response.DaemonURL = "/browser/proxy/" + registerBrowserTarget(internal, uuidToString(issue.WorkspaceID))
	} else {
		response.DaemonURL = daemonEditorBase(meta.EditorPort)
	}
	writeJSON(w, http.StatusOK, response)
	return true
}

func (h *Handler) GetIssueArtifact(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	run, err := h.Queries.GetLatestOrchestrationRunForIssue(r.Context(), issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		if h.writeLiveLocalArtifact(w, r, issue) {
			return
		}
		writeJSON(w, http.StatusOK, issueArtifactResponse{Ready: false, Reason: "no orchestration run", Components: []artifactSummary{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load artifact run failed")
		return
	}
	steps, err := h.Queries.ListOrchestrationSteps(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load artifact steps failed")
		return
	}
	runID := uuidToString(run.ID)
	canonical, canonicalOK := selectCanonicalArtifact(runID, steps)
	requestedStepID := strings.TrimSpace(r.URL.Query().Get("step_id"))
	response := issueArtifactResponse{
		RunID: runID, RunStatus: run.Status, Ready: canonicalOK,
		Components: artifactComponents(runID, steps, canonical.ID),
	}
	selected := canonical
	if !canonicalOK {
		response.Reason = "integration artifact is not ready"
		if requestedStepID == "" {
			// No orchestration artifact yet — prefer the live local working tree
			// when the project has a local_directory on an online daemon, so the
			// dev surface is usable during (and independent of) a run.
			if h.writeLiveLocalArtifact(w, r, issue) {
				return
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
		selected = artifactSummary{}
	}
	if requestedStepID != "" && requestedStepID != selected.StepID {
		found := false
		for _, component := range response.Components {
			if component.StepID == requestedStepID {
				selected = component
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "artifact step not found or not complete")
			return
		}
	}

	var selectedStep db.OrchestrationStep
	for _, step := range steps {
		if uuidToString(step.ID) == selected.StepID {
			selectedStep = step
			break
		}
	}
	if !selectedStep.TaskID.Valid {
		writeError(w, http.StatusConflict, "artifact has no runtime task")
		return
	}
	location, err := h.artifactRuntimeLocation(r.Context(), selectedStep.TaskID)
	if err != nil || strings.TrimSpace(location.WorkDir) == "" {
		writeJSON(w, http.StatusGone, map[string]string{"reason": "artifact_runtime_gone", "error": "The artifact runtime was cleaned up. Re-run integration to recreate it."})
		return
	}

	baseRecord := artifactCapabilityRecord{
		ArtifactID: selected.ID, WorkspaceID: uuidToString(issue.WorkspaceID),
		IssueID: uuidToString(issue.ID), RunID: runID, StepID: selected.StepID,
		RuntimeID: location.RuntimeID, DaemonID: location.DaemonID,
		SourceRoot: location.WorkDir, Repos: selected.Repos,
	}
	capabilities := make(map[string]string, 4)
	purposes := []string{"changes", "file"}
	if canonicalOK && selected.ID == canonical.ID {
		purposes = append(purposes, "preview", "checks")
	}
	for _, purpose := range purposes {
		record := baseRecord
		record.Purpose = purpose
		token, mintErr := mintArtifactCapability(record)
		if mintErr != nil {
			writeError(w, http.StatusInternalServerError, "mint artifact capability failed")
			return
		}
		capabilities[purpose] = token
	}

	internal := resolveDaemonInternalAddr(location.EditorAddr)
	if internal == "" {
		response.DaemonURL = daemonEditorBase(location.EditorPort)
	} else {
		response.DaemonURL = "/browser/proxy/" + registerBrowserTarget(internal, uuidToString(issue.WorkspaceID))
	}
	response.Artifact = &selected
	response.Capabilities = capabilities
	writeJSON(w, http.StatusOK, response)
}

// VerifyArtifactCapability is daemon-only introspection. The response contains
// the authoritative local source path and exact Git states; the browser only
// ever sees the opaque signed token.
func (h *Handler) VerifyArtifactCapability(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token   string `json:"token"`
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid capability request")
		return
	}
	record, ok := lookupArtifactCapability(req.Token)
	if !ok || record.Purpose != strings.TrimSpace(req.Purpose) {
		writeError(w, http.StatusForbidden, "invalid or expired artifact capability")
		return
	}

	workspaceID := middleware.DaemonWorkspaceIDFromContext(r.Context())
	daemonID := middleware.DaemonIDFromContext(r.Context())
	if workspaceID != "" {
		if workspaceID != record.WorkspaceID || (daemonID != "" && record.DaemonID != "" && daemonID != record.DaemonID) {
			writeError(w, http.StatusForbidden, "artifact capability is bound to another runtime")
			return
		}
	} else {
		userID, authenticated := requireUserID(w, r)
		if !authenticated {
			return
		}
		if _, err := h.getWorkspaceMember(r.Context(), userID, record.WorkspaceID); err != nil {
			writeError(w, http.StatusForbidden, "artifact capability is bound to another workspace")
			return
		}
	}
	writeJSON(w, http.StatusOK, record)
}
