package handler

import (
	"context"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/multica-ai/multica/server/internal/config"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Mid-run progress from a long autopilot into its Telegram group.
//
// The completed-run report answers "what happened". It does not answer "is it
// still going", which is the question a group actually has while a run that
// normally takes four minutes is twenty minutes in. Until now the only way to
// find out was to open Agora — so a group watching for a weekly report had no
// way to tell a slow run from a dead one.
//
// Built on the `PROGRESS:` headline the runtime brief already asks every agent
// to emit, rather than a new signal. An agent that has been taught to announce
// its own milestones is a better narrator than a timer, and reusing the
// existing contract means no agent has to learn anything new.
//
// Three rules keep this from becoming noise, which is the only way a feature
// like this fails:
//
//   - Autopilot runs only. A regular issue task narrating itself into a team
//     group would bury the messages that matter.
//   - Nothing before the run has been going a while. A run that finishes
//     quickly should produce exactly one message: its report.
//   - One update per interval, and only when the headline actually changed.
//     A group that gets a message per step stops reading them, and then the
//     one that mattered is missed too.

// telegramProgressStartDelay is how long a run must have been going before its
// first progress post. Set above the length of an ordinary run: the point is to
// speak up about the slow ones, not to narrate the normal ones.
const telegramProgressStartDelay = 5 * time.Minute

// telegramProgressInterval is the minimum gap between posts for one run.
const telegramProgressInterval = 5 * time.Minute

// progressRelayState remembers what was already said about a run, so a repeated
// headline does not become a repeated message.
type progressRelayState struct {
	mu   sync.Mutex
	last map[string]progressRelayEntry
}

type progressRelayEntry struct {
	headline string
	postedAt time.Time
}

var telegramProgressRelay = &progressRelayState{last: map[string]progressRelayEntry{}}

// forget drops a run's state once it ends. Without this the map grows for the
// life of the process — small per entry, unbounded over weeks.
func (s *progressRelayState) forget(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.last, runID)
}

// shouldPost decides whether this headline is worth a message, and records the
// decision. Returns false for a repeat, for a post inside the interval, and for
// the first headline of a run that has not been going long enough.
func (s *progressRelayState) shouldPost(runID, headline string, startedAt, now time.Time) bool {
	if now.Sub(startedAt) < telegramProgressStartDelay {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, seen := s.last[runID]
	if seen {
		if prev.headline == headline {
			return false
		}
		if now.Sub(prev.postedAt) < telegramProgressInterval {
			return false
		}
	}
	s.last[runID] = progressRelayEntry{headline: headline, postedAt: now}
	return true
}

// progressHeadline extracts the agent's own `PROGRESS:` line.
//
// Deliberately not line-anchored: agents write the marker mid-sentence often
// enough that requiring it at the start of a line drops exactly the updates
// worth relaying — the same mistake already made once with the QA phase
// markers, and fixed there for the same reason.
func progressHeadline(content string) string {
	idx := strings.Index(content, "PROGRESS:")
	if idx < 0 {
		return ""
	}
	rest := content[idx+len("PROGRESS:"):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	return strings.TrimSpace(rest)
}

// RelayAutopilotProgress posts one mid-run progress line to the autopilot's
// Telegram chat. Safe to call for every task message: it no-ops for non-
// autopilot tasks, for messages with no headline, and whenever the throttle
// says the group has heard enough.
//
// Best-effort throughout. A failed relay must never affect the run.
func (h *Handler) RelayAutopilotProgress(ctx context.Context, taskID, issueID, content string) {
	headline := progressHeadline(content)
	if headline == "" {
		return
	}
	// Both keys are offered because the two execution modes bind differently:
	// run_only sets the run's task_id, create_issue only ever sets issue_id.
	params := db.GetActiveAutopilotRunForTaskOrIssueParams{}
	if u, err := util.ParseUUID(taskID); err == nil {
		params.TaskID = u
	}
	if u, err := util.ParseUUID(issueID); err == nil {
		params.IssueID = u
	}
	if !params.TaskID.Valid && !params.IssueID.Valid {
		return
	}
	run, err := h.Queries.GetActiveAutopilotRunForTaskOrIssue(ctx, params)
	if err != nil {
		// Debug rather than silence: "the group got no update" is a question
		// operators do ask, and every skip below is a plausible answer.
		slog.Debug("progress relay: no live autopilot run", "task_id", taskID, "issue_id", issueID)
		return
	}
	ap, err := h.Queries.GetAutopilot(ctx, run.AutopilotID)
	if err != nil {
		return
	}
	// Opt-in, and per project: posting into a team group is outward-facing, so
	// a workspace should not start narrating its runs because it upgraded.
	if !h.autopilotProgressEnabled(ctx, ap) {
		slog.Debug("progress relay: disabled for this autopilot", "autopilot", ap.Title)
		return
	}

	if !telegramProgressRelay.shouldPost(
		uuidToString(run.ID), headline, run.TriggeredAt.Time, time.Now()) {
		slog.Debug("progress relay: throttled", "run_id", uuidToString(run.ID),
			"elapsed", time.Since(run.TriggeredAt.Time).String())
		return
	}

	// Same pairing rule as the report: a bot and a chat it can actually reach
	// are one decision. See autopilotDestination.
	bot, chatID := h.autopilotDestination(ctx, ap)
	if bot == nil || chatID == "" {
		slog.Debug("progress relay: nowhere to send", "autopilot", ap.Title,
			"has_bot", bot != nil, "chat", chatID)
		return
	}
	// Named so the group can tell which of several autopilots is speaking, and
	// kept to one line: this is a status ping, not a report.
	text := ap.Title + ": " + truncateRunes(headline, 200)
	if err := bot.SendMessage(ctx, chatID, text); err != nil {
		slog.Debug("autopilot progress relay: send failed", "run_id", uuidToString(run.ID), "error", err)
		return
	}
	slog.Info("autopilot progress posted", "run_id", uuidToString(run.ID),
		"autopilot", ap.Title, "chat", chatID)
}

// ForgetAutopilotProgress clears a finished run's throttle state.
func (h *Handler) ForgetAutopilotProgress(runID string) {
	telegramProgressRelay.forget(runID)
}

// autopilotProgressEnabled reads the flag, honouring a project override the
// same way the report chat does — a project that wants running commentary
// should be able to have it without every other project starting to talk.
func (h *Handler) autopilotProgressEnabled(ctx context.Context, ap db.Autopilot) bool {
	var overrides map[string]string
	if ap.ProjectID.Valid {
		if project, err := h.Queries.GetProject(ctx, ap.ProjectID); err == nil {
			overrides = parseProjectConfigOverrides(project.Settings)
		}
	}
	return config.BoolFrom(overrides, "AGORA_TELEGRAM_PROGRESS_ENABLED")
}
