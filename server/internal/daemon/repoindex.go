package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/repoindex"
)

// Repo context pack: the "push" half of Agora's index-augmented agent work.
//
// At dispatch the daemon ranks the task's repository against the issue text
// and prepends a compact map of the likely-relevant files to the prompt, so
// the agent can skip rediscovering the tree. This is push-only by design —
// see the package comment on internal/repoindex for the measurements that
// ruled out a pull-style tool surface.
//
// Everything here fails soft. A pack is an optimization; a task must run
// exactly as it does today when the pack is empty, slow, or broken.

const (
	// packBuildTimeout bounds the scan. A repo that can't be ranked in this
	// long doesn't get a pack — the agent starts on time regardless. Plan
	// rule: never block agent start.
	packBuildTimeout = 20 * time.Second

	// packArms is the A/B denominator. Arm 0 = control (no pack), arm 1 =
	// treatment. Randomizing at the TASK boundary (not per-daemon, not
	// per-agent) is what makes the comparison valid: both arms run on the
	// same runtimes, models, repos, and task mix.
	packArms = 2
)

// repoIndexDisabled reports the operator opt-out. Mirrors the QA MCP
// server's AGORA_QA_MCP_DISABLED convention.
func repoIndexDisabled() bool {
	return os.Getenv("AGORA_REPO_INDEX_DISABLED") == "1"
}

// packArm assigns a task to an experiment arm deterministically from its ID.
//
// Deterministic hashing (not a random draw) means a retried or resumed task
// keeps its arm, so a single task can never contribute to both arms and
// contaminate the comparison.
func packArm(taskID string) int {
	sum := sha256.Sum256([]byte("repo-index-pack:" + taskID))
	return int(binary.BigEndian.Uint64(sum[:8]) % packArms)
}

// taskWantsRepoPack reports whether this task type reads code at all.
//
// The pack costs prompt tokens on every task that gets it, so it must only go
// where a repository map could plausibly pay for itself. Quick-create (no
// issue, no repo), chat, and autopilot dispatches are excluded. A tagged
// comment reply is excluded only when it actually resumed a warm provider
// session; a newly tagged agent has no code context yet and needs the pack.
func taskWantsRepoPack(task Task) bool {
	switch {
	case task.QuickCreatePrompt != "":
		return false
	case task.ChatSessionID != "":
		return false
	case task.AutopilotRunID != "":
		return false
	case task.TriggerCommentID != "" && task.PriorSessionID != "":
		return false
	case task.IssueID == "":
		return false
	}
	return true
}

// taskRequiresRepoPack identifies work where code context is part of the
// correctness contract rather than an experiment: persisted orchestration
// stages and a newly tagged agent's first issue turn. These tasks always use
// the treatment arm so a worker is not asked to guess repository structure.
func taskRequiresRepoPack(task Task) bool {
	return task.OrchestrationStepID != "" || (task.TriggerCommentID != "" && task.PriorSessionID == "")
}

// packQuery builds the retrieval query from what the claim carried. Title
// alone is a usable query; the description sharpens it considerably. Against
// an older server IssueBody is empty and the title carries the ranking.
func packQuery(task Task) string {
	var b strings.Builder
	b.WriteString(task.ThreadName)
	if task.IssueBody != "" {
		b.WriteString("\n")
		b.WriteString(task.IssueBody)
	}
	// The project title is weak signal but free, and it disambiguates
	// monorepo tasks ("sd-main" vs "sd-bridge") when the issue text is terse.
	if task.ProjectTitle != "" {
		b.WriteString("\n")
		b.WriteString(task.ProjectTitle)
	}
	return b.String()
}

// buildRepoPack returns the region to prepend to the prompt, plus the stats to
// report. An empty string means "inject nothing" — every failure path lands
// here, and none of them is fatal.
//
// Stats are returned for BOTH arms (nil only when the task was never eligible
// for a pack). A control task that is silently unrecorded would leave the
// experiment with no denominator, so eligibility, not treatment, is what
// decides whether a task is measured.
//
// workDir must already have cleared the daemon's execution gates: for a
// local_directory task that is validateLocalPath + checkLocalDirApproved (the
// owner's consent record on this machine), and for a managed repo it is a
// daemon-created workspace clone. This function does not re-derive consent —
// it indexes the directory the task is already running in, which is exactly
// the directory the agent may already read.
func (d *Daemon) buildRepoPack(ctx context.Context, task Task, workDir string, taskLog *slog.Logger) (string, *TaskContextPackStats) {
	if repoIndexDisabled() || workDir == "" || !taskWantsRepoPack(task) {
		return "", nil // not eligible: outside the experiment entirely
	}

	arm := packArm(task.ID)
	if taskRequiresRepoPack(task) {
		arm = 1
	}
	if arm == 0 {
		// Control arm: no pack, no hint, nothing. A control task must be
		// indistinguishable from today's behavior — but it IS recorded, so
		// the comparison has both sides.
		return "", &TaskContextPackStats{Arm: arm}
	}

	ctx, cancel := context.WithTimeout(ctx, packBuildTimeout)
	defer cancel()

	start := time.Now()
	pack, packStats, err := repoindex.Pack(ctx, workDir, packQuery(task), repoindex.DefaultTokenBudget)
	elapsed := time.Since(start)

	stats := &TaskContextPackStats{
		Arm:           arm,
		FilesScanned:  packStats.FilesScanned,
		FilesInPack:   packStats.FilesInPack,
		SymbolsInPack: packStats.SymbolsInPack,
		PackTokens:    packStats.PackTokens,
		BuildMs:       int(elapsed.Milliseconds()),
		Degraded:      packStats.Degraded,
		Partial:       packStats.Partial,
	}
	if err != nil {
		stats.Degraded = true
		taskLog.Warn("repo-index: pack build failed (non-fatal, task continues without it)",
			"error", err, "work_dir", workDir, "elapsed", elapsed)
		return "", stats
	}
	if pack == "" {
		taskLog.Debug("repo-index: no pack for this task",
			"files_scanned", packStats.FilesScanned, "elapsed", elapsed)
		return "", stats
	}
	taskLog.Info("repo-index: pack built",
		"files_scanned", packStats.FilesScanned,
		"files_in_pack", packStats.FilesInPack,
		"pack_tokens", packStats.PackTokens,
		"elapsed_ms", elapsed.Milliseconds(),
	)
	return pack, stats
}
