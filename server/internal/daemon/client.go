package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// requestError is returned by postJSON/getJSON when the server responds with an error status.
type requestError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *requestError) Error() string {
	body := strings.TrimSpace(e.Body)
	lower := strings.ToLower(body)
	if strings.Contains(lower, "<title>blocked</title>") {
		body = "hosted edge blocked the request"
	} else if len(body) > 512 {
		body = body[:512] + "..."
	}
	return fmt.Sprintf("%s %s returned %d: %s", e.Method, e.Path, e.StatusCode, body)
}

// isWorkspaceNotFoundError returns true if the error is a 404 with "workspace not found" body.
func isWorkspaceNotFoundError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "workspace not found")
}

// isTaskNotFoundError returns true if the error is a 404 with "task not found"
// body. The daemon uses this to detect that a task was deleted server-side
// (issue removed, agent reassigned, ...) while the local agent was still
// running, so it can interrupt the agent rather than letting it keep
// emitting tool calls against a dead task.
func isTaskNotFoundError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "task not found")
}

// isUnauthorizedError returns true if the error is a 401 from the server.
// Used by the token-renewal loop to surface a clear "re-login required"
// message instead of a generic transport-level retry.
func isUnauthorizedError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	return reqErr.StatusCode == http.StatusUnauthorized
}

// isRuntimeNotFoundError returns true if the error is a 404 with "runtime not
// found" body. The daemon uses this to detect that the runtime row was deleted
// server-side (UI Delete, 7-day offline GC) while the daemon was still
// heartbeating against the dead UUID, so it can prune the stale runtime from
// its local state and re-register instead of looping on the dead ID forever.
//
// Server-side, this body is paired with pgx.ErrNoRows specifically (other DB
// errors return 500), so a transient DB hiccup cannot make the daemon
// self-cleanup.
func isRuntimeNotFoundError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "runtime not found")
}

// Client handles HTTP communication with the Agora server daemon API.
type Client struct {
	baseURL string
	token   string
	client  *http.Client

	// Identity headers sent on every request as X-Client-*. Populated by
	// SetIdentity(); empty values are simply omitted.
	platform string
	version  string
	os       string
}

// NewClient creates a new daemon API client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:  baseURL,
		client:   &http.Client{Timeout: 30 * time.Second},
		platform: "daemon",
		os:       normalizeGOOS(runtime.GOOS),
	}
}

// normalizeGOOS maps Go's runtime.GOOS values to the protocol vocabulary
// used by X-Client-OS / client_os ("macos" / "windows" / "linux").
func normalizeGOOS(goos string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	default:
		return goos
	}
}

// SetVersion records the daemon's CLI version, sent as X-Client-Version.
// Called by Daemon.Run after config is loaded.
func (c *Client) SetVersion(v string) {
	c.version = v
}

// setIdentityHeaders attaches X-Client-Platform/Version/OS to req when set.
func (c *Client) setIdentityHeaders(req *http.Request) {
	if c.platform != "" {
		req.Header.Set("X-Client-Platform", c.platform)
	}
	if c.version != "" {
		req.Header.Set("X-Client-Version", c.version)
	}
	if c.os != "" {
		req.Header.Set("X-Client-OS", c.os)
	}
}

// SetToken sets the auth token for authenticated requests.
func (c *Client) SetToken(token string) {
	c.token = token
}

// Token returns the current auth token.
func (c *Client) Token() string {
	return c.token
}

// VerifyArtifactCapability asks the Agora control plane to introspect an opaque
// browser capability. Local source paths and exact Git refs are returned only
// to the authenticated daemon, never to the browser.
func (c *Client) VerifyArtifactCapability(ctx context.Context, token, purpose string) (ArtifactCapabilityGrant, error) {
	var grant ArtifactCapabilityGrant
	err := c.postJSON(ctx, "/api/daemon/artifact-capabilities/verify", map[string]string{
		"token": token, "purpose": purpose,
	}, &grant)
	return grant, err
}

func (c *Client) ClaimTask(ctx context.Context, runtimeID string) (*Task, error) {
	var resp struct {
		Task *Task `json:"task"`
	}
	if err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/tasks/claim", runtimeID), map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Task, nil
}

func (c *Client) StartTask(ctx context.Context, taskID string) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/start", taskID), map[string]any{}, nil)
}

// MarkTaskWaitingLocalDirectory parks a freshly-dispatched task in the
// waiting_local_directory state on the server. The daemon calls this after
// it has claimed a task whose project carries a local_directory resource
// but the path mutex is held by another in-flight task. reason is a short
// human-readable hint (e.g. "<path>") surfaced by the UI alongside the
// status. Idempotent on the daemon's side — calling twice with the same
// reason is a no-op once the row is already waiting_local_directory (the
// underlying SQL filters on status='dispatched', so the second call is a
// 400 the daemon swallows and proceeds to wait).
func (c *Client) MarkTaskWaitingLocalDirectory(ctx context.Context, taskID, reason string) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/wait-local-directory", taskID), map[string]any{
		"reason": reason,
	}, nil)
}

func (c *Client) ReportProgress(ctx context.Context, taskID, summary string, step, total int) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/progress", taskID), map[string]any{
		"summary": summary,
		"step":    step,
		"total":   total,
	}, nil)
}

// TaskMessageData represents a single agent execution message for batch reporting.
type TaskMessageData struct {
	Seq     int            `json:"seq"`
	Type    string         `json:"type"`
	Tool    string         `json:"tool,omitempty"`
	Content string         `json:"content,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Output  string         `json:"output,omitempty"`
}

func (c *Client) ReportTaskMessages(ctx context.Context, taskID string, messages []TaskMessageData) error {
	if len(messages) == 0 {
		return nil
	}
	bounded := make([]TaskMessageData, len(messages))
	for i, message := range messages {
		bounded[i] = boundTaskMessage(message)
	}
	return c.reportTaskMessageBatch(ctx, taskID, bounded)
}

const (
	taskMessageContentLimit = 32 * 1024
	taskMessageDetailLimit  = 8 * 1024
	taskMessageInputLimit   = 12 * 1024
	taskMessageBatchLimit   = 48 * 1024
	edgeOmittedDetail       = "[Task detail omitted because the hosted edge rejected the payload.]"
)

func boundTaskMessage(message TaskMessageData) TaskMessageData {
	message.Content = truncateTaskMessageField(message.Content, taskMessageContentLimit)
	message.Output = truncateTaskMessageField(message.Output, taskMessageDetailLimit)
	if message.Input != nil {
		if data, err := json.Marshal(message.Input); err != nil || len(data) > taskMessageInputLimit {
			message.Input = map[string]any{"detail": "[Tool input omitted because it exceeded the transcript limit.]"}
		}
	}
	return message
}

func truncateTaskMessageField(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[Transcript detail truncated.]"
}

// reportTaskMessageBatch keeps one rejected transcript item from blocking all
// subsequent live output. Large batches are split before they reach the edge;
// edge-rejected batches are bisected until the offending singleton is found.
// The singleton keeps its idempotency sequence and type but drops only the
// detail that triggered the hosted WAF. Arbitrary authorization failures are
// returned unchanged and never rewritten as content failures.
func (c *Client) reportTaskMessageBatch(ctx context.Context, taskID string, messages []TaskMessageData) error {
	if len(messages) == 0 {
		return nil
	}
	if len(messages) > 1 {
		if data, err := json.Marshal(messages); err == nil && len(data) > taskMessageBatchLimit {
			mid := len(messages) / 2
			if err := c.reportTaskMessageBatch(ctx, taskID, messages[:mid]); err != nil {
				return err
			}
			return c.reportTaskMessageBatch(ctx, taskID, messages[mid:])
		}
	}

	path := fmt.Sprintf("/api/daemon/tasks/%s/messages", taskID)
	post := func(batch []TaskMessageData) error {
		return c.postJSONWithRetry(ctx, path, map[string]any{"messages": batch}, nil, defaultTaskMessageRetrySchedule)
	}
	if err := post(messages); err != nil {
		if !isEdgePayloadRejection(err) {
			return err
		}
		if len(messages) > 1 {
			mid := len(messages) / 2
			if splitErr := c.reportTaskMessageBatch(ctx, taskID, messages[:mid]); splitErr != nil {
				return splitErr
			}
			return c.reportTaskMessageBatch(ctx, taskID, messages[mid:])
		}
		return post([]TaskMessageData{omitTaskMessageDetail(messages[0])})
	}
	return nil
}

func isEdgePayloadRejection(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode == http.StatusRequestEntityTooLarge {
		return true
	}
	return reqErr.StatusCode == http.StatusForbidden &&
		strings.Contains(strings.ToLower(reqErr.Body), "<title>blocked</title>")
}

func omitTaskMessageDetail(message TaskMessageData) TaskMessageData {
	omitted := TaskMessageData{Seq: message.Seq, Type: message.Type, Tool: message.Tool}
	switch message.Type {
	case "tool_use":
		omitted.Input = map[string]any{"detail": edgeOmittedDetail}
	case "tool_result":
		omitted.Output = edgeOmittedDetail
	default:
		omitted.Content = edgeOmittedDetail
	}
	return omitted
}

func (c *Client) CompleteTask(ctx context.Context, taskID, output, branchName, sessionID, workDir, baseSHA, headSHA, mergeStatus string, conflictFiles []string, integrationStatus string, integratedHeadSHAs []string, integratedHeads []OrchestrationGitHead, missingHeadSHAs []string, gitStates []RepoGitState) error {
	body := map[string]any{"output": output}
	if branchName != "" {
		body["branch_name"] = branchName
	}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	if workDir != "" {
		body["work_dir"] = workDir
	}
	if baseSHA != "" {
		body["base_sha"] = baseSHA
	}
	if headSHA != "" {
		body["head_sha"] = headSHA
	}
	if mergeStatus != "" {
		body["merge_status"] = mergeStatus
	}
	if len(conflictFiles) > 0 {
		body["conflict_files"] = conflictFiles
	}
	if integrationStatus != "" {
		body["integration_status"] = integrationStatus
	}
	if len(integratedHeadSHAs) > 0 {
		body["integrated_head_shas"] = integratedHeadSHAs
	}
	if len(integratedHeads) > 0 {
		body["integrated_heads"] = integratedHeads
	}
	if len(missingHeadSHAs) > 0 {
		body["missing_head_shas"] = missingHeadSHAs
	}
	if len(gitStates) > 0 {
		body["git_states"] = gitStates
	}
	return c.postJSONWithRetry(ctx, fmt.Sprintf("/api/daemon/tasks/%s/complete", taskID), body, nil, defaultTerminalRetrySchedule)
}

// PinOrchestrationBase proposes the local repository HEADs for an
// orchestration run. The server atomically accepts the first proposal and
// always returns the canonical snapshot so parallel workers converge.
func (c *Client) PinOrchestrationBase(ctx context.Context, taskID string, proposed []OrchestrationGitHead) ([]OrchestrationGitHead, error) {
	var response struct {
		BaseRefs []OrchestrationGitHead `json:"base_refs"`
	}
	if err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/orchestration-base", taskID), map[string]any{"base_refs": proposed}, &response); err != nil {
		return nil, err
	}
	return response.BaseRefs, nil
}

// ReportTaskUsage reports per-model token usage and, when the task was part of
// the repo-context-pack experiment, that task's pack stats. Both ride the same
// request because both are per-task telemetry emitted at the same moment;
// contextPack is optional and older servers ignore the field.
func (c *Client) ReportTaskUsage(ctx context.Context, taskID string, usage []TaskUsageEntry, contextPack *TaskContextPackStats) error {
	if len(usage) == 0 && contextPack == nil {
		return nil
	}
	body := map[string]any{"usage": usage}
	if contextPack != nil {
		body["context_pack"] = contextPack
	}
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/usage", taskID), body, nil)
}

func (c *Client) FailTask(ctx context.Context, taskID, errMsg, sessionID, workDir, failureReason string) error {
	body := map[string]any{"error": errMsg}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	if workDir != "" {
		body["work_dir"] = workDir
	}
	if failureReason != "" {
		body["failure_reason"] = failureReason
	}
	return c.postJSONWithRetry(ctx, fmt.Sprintf("/api/daemon/tasks/%s/fail", taskID), body, nil, defaultTerminalRetrySchedule)
}

// PinTaskSession persists the agent's session_id and work_dir on the task
// row mid-flight so a daemon crash doesn't lose the resume pointer.
func (c *Client) PinTaskSession(ctx context.Context, taskID, sessionID, workDir string) error {
	if sessionID == "" && workDir == "" {
		return nil
	}
	body := map[string]any{}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	if workDir != "" {
		body["work_dir"] = workDir
	}
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/session", taskID), body, nil)
}

// RecoverOrphans tells the server to fail any dispatched/running tasks the
// previous daemon process for this runtime left behind. The server will
// auto-retry eligible tasks.
func (c *Client) RecoverOrphans(ctx context.Context, runtimeID string) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/recover-orphans", runtimeID), map[string]any{}, nil)
}

// GetTaskStatus returns the current status of a task. Used by the daemon to
// detect terminal/interruption signals (cancelled, failed, completed, or a
// 404 task-not-found) while a task is executing.
func (c *Client) GetTaskStatus(ctx context.Context, taskID string) (string, error) {
	var resp struct {
		Status string `json:"status"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/status", taskID), &resp); err != nil {
		return "", err
	}
	return resp.Status, nil
}

// HeartbeatResponse, PendingUpdate, etc. alias the wire types so HTTP and WS
// heartbeat paths share a single type and a single decoder shape. Aliases
// (rather than wrappers) keep call sites unchanged.
type (
	HeartbeatResponse       = protocol.DaemonHeartbeatAckPayload
	PendingUpdate           = protocol.DaemonHeartbeatPendingUpdate
	PendingModelList        = protocol.DaemonHeartbeatPendingModelList
	PendingLocalSkills      = protocol.DaemonHeartbeatPendingLocalSkills
	PendingLocalSkillImport = protocol.DaemonHeartbeatPendingLocalSkillImport
)

func (c *Client) SendHeartbeat(ctx context.Context, runtimeID string) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	if err := c.postJSON(ctx, "/api/daemon/heartbeat", map[string]any{
		"runtime_id":            runtimeID,
		"supports_batch_import": true,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ReportUpdateResult sends the CLI update result back to the server.
func (c *Client) ReportUpdateResult(ctx context.Context, runtimeID, updateID string, result map[string]any) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/update/%s/result", runtimeID, updateID), result, nil)
}

// ReportModelListResult sends the model-discovery result back to the server.
func (c *Client) ReportModelListResult(ctx context.Context, runtimeID, requestID string, result map[string]any) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/models/%s/result", runtimeID, requestID), result, nil)
}

// ReportLocalSkillListResult sends the runtime-local-skill inventory back to the server.
func (c *Client) ReportLocalSkillListResult(ctx context.Context, runtimeID, requestID string, result map[string]any) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/local-skills/%s/result", runtimeID, requestID), result, nil)
}

// ReportLocalSkillImportResult sends a runtime-local-skill bundle back to the server.
func (c *Client) ReportLocalSkillImportResult(ctx context.Context, runtimeID, requestID string, result map[string]any) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/local-skills/import/%s/result", runtimeID, requestID), result, nil)
}

// WorkspaceInfo holds minimal workspace metadata returned by the API.
type WorkspaceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RenewTokenResponse mirrors handler.RenewPATResponse — kept loose (string +
// bool) because the daemon never parses the timestamp itself; it just logs it
// for operator visibility.
type RenewTokenResponse struct {
	ExpiresAt string `json:"expires_at"`
	Renewed   bool   `json:"renewed"`
}

// RenewToken asks the server to extend the daemon's current PAT in place when
// it's within the server-side renewal window. The server is authoritative on
// the threshold — the daemon doesn't know the token's expires_at locally —
// so this is safe to call on any cadence; the only thing extra calls cost is
// one round trip and one cheap SELECT.
func (c *Client) RenewToken(ctx context.Context) (*RenewTokenResponse, error) {
	var resp RenewTokenResponse
	if err := c.postJSON(ctx, "/api/tokens/current/renew", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListWorkspaces fetches all workspaces the authenticated user belongs to.
func (c *Client) ListWorkspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	var workspaces []WorkspaceInfo
	if err := c.getJSON(ctx, "/api/workspaces", &workspaces); err != nil {
		return nil, err
	}
	return workspaces, nil
}

// IssueGCStatus holds the minimal issue info returned by the GC check endpoint.
type IssueGCStatus struct {
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetIssueGCCheck returns the status and updated_at of an issue for GC decisions.
func (c *Client) GetIssueGCCheck(ctx context.Context, issueID string) (*IssueGCStatus, error) {
	var resp IssueGCStatus
	if err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/issues/%s/gc-check", issueID), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ChatSessionGCStatus mirrors IssueGCStatus for chat sessions.
type ChatSessionGCStatus struct {
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetChatSessionGCCheck returns the status of a chat session for GC decisions.
// A 404 from this endpoint indicates the session row was hard-deleted (the
// user explicitly removed it), which the caller treats as an immediate-clean
// signal.
func (c *Client) GetChatSessionGCCheck(ctx context.Context, sessionID string) (*ChatSessionGCStatus, error) {
	var resp ChatSessionGCStatus
	if err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/chat-sessions/%s/gc-check", sessionID), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AutopilotRunGCStatus carries the status of an autopilot run. CompletedAt
// is the run's terminal timestamp (zero for non-terminal runs); the GC loop
// uses it as the TTL anchor instead of UpdatedAt because autopilot_run rows
// have no updated_at column.
type AutopilotRunGCStatus struct {
	Status      string    `json:"status"`
	CompletedAt time.Time `json:"completed_at"`
}

// GetAutopilotRunGCCheck returns the status of an autopilot run for GC decisions.
func (c *Client) GetAutopilotRunGCCheck(ctx context.Context, runID string) (*AutopilotRunGCStatus, error) {
	var resp AutopilotRunGCStatus
	if err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/autopilot-runs/%s/gc-check", runID), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// TaskGCStatus carries the agent_task_queue status for quick-create cleanup.
// Quick-create tasks have no separate parent record, so GC keys directly on
// the task itself.
type TaskGCStatus struct {
	Status      string    `json:"status"`
	CompletedAt time.Time `json:"completed_at"`
}

// GetTaskGCCheck returns the status of an agent task for GC decisions.
func (c *Client) GetTaskGCCheck(ctx context.Context, taskID string) (*TaskGCStatus, error) {
	var resp TaskGCStatus
	if err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/gc-check", taskID), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Deregister(ctx context.Context, runtimeIDs []string) error {
	return c.postJSON(ctx, "/api/daemon/deregister", map[string]any{
		"runtime_ids": runtimeIDs,
	}, nil)
}

// RegisterResponse holds the server's response to a daemon registration.
type RegisterResponse struct {
	Runtimes     []Runtime       `json:"runtimes"`
	Repos        []RepoData      `json:"repos"`
	ReposVersion string          `json:"repos_version"`
	Settings     json.RawMessage `json:"settings,omitempty"`
}

func (c *Client) Register(ctx context.Context, req map[string]any) (*RegisterResponse, error) {
	var resp RegisterResponse
	if err := c.postJSON(ctx, "/api/daemon/register", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

type WorkspaceReposResponse struct {
	WorkspaceID  string          `json:"workspace_id"`
	Repos        []RepoData      `json:"repos"`
	ReposVersion string          `json:"repos_version"`
	Settings     json.RawMessage `json:"settings,omitempty"`
}

func (c *Client) GetWorkspaceRepos(ctx context.Context, workspaceID string) (*WorkspaceReposResponse, error) {
	var resp WorkspaceReposResponse
	if err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/workspaces/%s/repos", workspaceID), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// defaultTerminalRetrySchedule is the backoff used by postJSONWithRetry for
// terminal task callbacks (CompleteTask / FailTask). N entries → N+1 attempts
// in the worst case (one immediate + N retries). Five backoffs totalling
// 124s is wide enough to ride out the short upstream blips we've seen
// (MUL-2780) without leaving the task stuck if the outage outlives the
// window.
var defaultTerminalRetrySchedule = []time.Duration{
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
	64 * time.Second,
}

// defaultTaskMessageRetrySchedule covers short transport and upstream blips
// without holding the live transcript sender for the terminal callback's full
// two-minute retry window. The server treats (task_id, seq) as an idempotency
// key, so retrying an ambiguously-acknowledged batch cannot duplicate rows.
var defaultTaskMessageRetrySchedule = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
}

// retrySleep is the sleep used between retry attempts. Pulled into a package
// variable so tests can swap in an instant sleep without rewriting the
// caller's schedule.
var retrySleep = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isTransientError reports whether err looks like a hiccup that's likely to
// resolve on retry: connection / TLS / I/O errors at the transport layer
// (including client timeouts surfacing as context.DeadlineExceeded inside
// http.Client.Do), 5xx server responses, and 408/429 rate-limit-style 4xx
// codes. Other 4xx codes are treated as permanent — retrying a 400 (bad
// body) or 404 (task not found) only burns time.
//
// The caller is responsible for separately bailing on parent-context
// cancellation; this predicate cannot distinguish "the daemon is shutting
// down" from "the HTTP client timed out a single attempt" because both
// reach here as context errors wrapped by net/http.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	var reqErr *requestError
	if errors.As(err, &reqErr) {
		if reqErr.StatusCode >= 500 {
			return true
		}
		if reqErr.StatusCode == http.StatusRequestTimeout || reqErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		return false
	}
	return true
}

// postJSONWithRetry posts a JSON body with bounded exponential backoff,
// intended for "must reach the server" terminal callbacks (CompleteTask /
// FailTask). It retries transient errors per isTransientError and stops
// immediately on permanent 4xx responses so we don't burn the schedule on
// requests the server has already rejected.
//
// schedule controls the sleeps between attempts. With N entries the helper
// performs N+1 attempts in the worst case (one initial + N retries). The
// returned error is the last response from the server, so callers can still
// inspect it with isTransientError to decide whether to fall back to a
// different terminal call (e.g. complete → fail on permanent error only).
//
// The server-side CompleteTask / FailTask treat "already terminal" as an
// idempotent success (see service/task.go), so a duplicate replay from a
// retry is safe even if the server's prior response was lost in transit.
func (c *Client) postJSONWithRetry(ctx context.Context, path string, reqBody any, respBody any, schedule []time.Duration) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		err := c.postJSON(ctx, path, reqBody, respBody)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientError(err) {
			return err
		}
		if attempt >= len(schedule) {
			return err
		}
		if sleepErr := retrySleep(ctx, schedule[attempt]); sleepErr != nil {
			return err
		}
	}
}

func (c *Client) postJSON(ctx context.Context, path string, reqBody any, respBody any) error {
	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.setIdentityHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &requestError{Method: http.MethodPost, Path: path, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if respBody == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(respBody)
}

func (c *Client) getJSON(ctx context.Context, path string, respBody any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.setIdentityHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &requestError{Method: http.MethodGet, Path: path, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if respBody == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(respBody)
}
