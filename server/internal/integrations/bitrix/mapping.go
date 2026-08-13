package bitrix

import (
	"strings"
	"time"
)

// aiTag is the tag (case-insensitive) that marks a Bitrix task as one Agora
// should sync. Bitrix stays the task master; only tasks explicitly flagged
// "ai" cross into Agora so the board isn't flooded with the portal's full
// task list.
const aiTag = "ai"

// Agora issue statuses. Kept as plain strings (not an enum) because the
// canonical list lives in the DB / handler layer; these are the values the
// mapping emits.
const (
	StatusBacklog    = "backlog"
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusInReview   = "in_review"
	StatusDone       = "done"
	StatusBlocked    = "blocked"
	StatusCancelled  = "cancelled"
)

// IsAITask reports whether the task carries the "ai" tag (case-insensitive).
// Whitespace around a tag is ignored so " AI " still matches.
func IsAITask(task *Task) bool {
	if task == nil {
		return false
	}
	for _, t := range task.Tags {
		if strings.EqualFold(strings.TrimSpace(t), aiTag) {
			return true
		}
	}
	return false
}

// IsClosedStatus reports whether a Bitrix STATUS/REAL_STATUS code means the
// task is finished on the portal (completed or declined). Personal mirrors
// skip creating these and remove any previously synced Agora issue.
func IsClosedStatus(bitrixStatus string) bool {
	switch strings.TrimSpace(bitrixStatus) {
	case "5", "7":
		return true
	default:
		return false
	}
}

// IsClosedIssueStatus reports whether an Agora status is a terminal board
// column that must not stay on a personal Bitrix mirror.
func IsClosedIssueStatus(agoraStatus string) bool {
	switch strings.TrimSpace(agoraStatus) {
	case StatusDone, StatusCancelled:
		return true
	default:
		return false
	}
}

// MapStatus maps a Bitrix task status code to a Agora issue status.
//
// Bitrix task status codes (REAL_STATUS / STATUS):
//
//	1 = new                  -> todo
//	2 = pending / awaiting    -> todo
//	3 = in progress           -> in_progress
//	4 = supposedly completed  -> in_review   (awaiting acceptance)
//	5 = completed             -> done
//	6 = deferred              -> backlog
//	7 = declined              -> cancelled
//
// Anything unrecognized (including empty) defaults to "todo" so a task always
// lands somewhere actionable rather than being dropped.
func MapStatus(bitrixStatus string) string {
	switch strings.TrimSpace(bitrixStatus) {
	case "1", "2":
		return StatusTodo
	case "3":
		return StatusInProgress
	case "4":
		return StatusInReview
	case "5":
		return StatusDone
	case "6":
		return StatusBacklog
	case "7":
		return StatusCancelled
	default:
		return StatusTodo
	}
}

// MapStage maps a Bitrix scrum/kanban STAGE name (the live column the dev team
// drags a task through — Новые / Code Review / Ready for release / Сделаны …) to
// an Agora status. Stages are matched by KEYWORD (substring, case-insensitive)
// because the exact labels differ per sprint group and mix Russian/English, with
// typos ("Ready fo release", "Reayd For Testing"). Returns "" for an empty or
// unrecognized stage so the caller falls back to the coarse STATUS mapping.
//
// ORDER IS LOAD-BEARING — each rule below is placed to beat a specific
// collision, so do not reorder without re-reading these:
//
//   - release BEFORE done: "Готов к релизу" contains "готов". A release-ready
//     task is awaiting deploy, not finished; mapping it to done also archives
//     the mirror (AGORA_BITRIX_ARCHIVE_DONE) and it disappears from the board.
//   - done matches "готово", never bare "готов", for the same reason.
//   - dev-testing BEFORE testing: the developer's own pass is still dev work,
//     while "Ready for testing"/"Testing" is handed to QA.
//   - todo carries "к выполнен" (К выполнению = To Do) which must not be
//     confused with "выполня" (Выполняются = in progress).
func MapStage(stage string) string {
	s := strings.ToLower(strings.TrimSpace(stage))
	if s == "" {
		return ""
	}
	switch {
	case containsAny(s, "fail", "cancel", "отмен", "отклон"):
		return StatusCancelled
	case containsAny(s, "block", "блок"):
		return StatusBlocked
	case containsAny(s, "release", "релиз"):
		// Ready for release / Ready fo release / Ready For Release / Готов к релизу
		// — code is finished and reviewed, the deploy is the remaining gate.
		return StatusInReview
	case containsAny(s, "сделан", "готово", "done", "complete", "завершен", "closed"):
		return StatusDone
	case containsAny(s, "dev test", "dev тест", "разработчик тест"):
		return StatusInProgress
	case containsAny(s, "review", "ревью", "ревю", "merg", "мерж"):
		return StatusInReview
	case containsAny(s, "test", "тест", "qa", "проверк"):
		return StatusInReview
	case containsAny(s, "return", "возврат", "вернул"):
		// QA/reviewer sent it back — the ball is with the developer again.
		return StatusInProgress
	case containsAny(s, "выполня", "progress", "doing", "develop", "разработ", "в работе", "процесс"):
		return StatusInProgress
	case containsAny(s, "нов", "new", "to do", "todo", "к выполнен", "unready", "given",
		"backlog", "бэклог", "очеред", "draft", "черновик", "обсужда", "discussion", "перенес"):
		return StatusTodo
	default:
		return ""
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// BitrixStatusFromIssue is the inverse of MapStatus: it maps a Agora issue
// status to the Bitrix status code used when mirroring a status change back to
// the portal. It is intentionally NOT a perfect round-trip (Bitrix has more
// codes than Agora statuses) — it picks the most faithful Bitrix code for
// each Agora status:
//
//	backlog     -> 6 (deferred)
//	todo        -> 2 (pending)
//	in_progress -> 3 (in progress)
//	blocked     -> 3 (in progress)  — Bitrix has no blocked code; the task is
//	                                  still open and owned, and the kanban stage
//	                                  move carries the "Blocker" signal instead.
//	in_review   -> 4 (supposedly completed / awaiting acceptance)
//	done        -> 5 (completed)
//	cancelled   -> 7 (declined)
//
// Unknown statuses default to "2" (pending), matching MapStatus's default
// landing zone.
func BitrixStatusFromIssue(issueStatus string) string {
	switch strings.TrimSpace(issueStatus) {
	case StatusBacklog:
		return "6"
	case StatusTodo:
		return "2"
	case StatusInProgress, StatusBlocked:
		return "3"
	case StatusInReview:
		return "4"
	case StatusDone:
		return "5"
	case StatusCancelled:
		return "7"
	default:
		return "2"
	}
}

// IssueDraft is the transport-agnostic shape of the issue a Bitrix task maps
// to. The handler turns it into IssueService.IssueCreateParams.
type IssueDraft struct {
	Title       string
	Description string
	Status      string
}

// MapTaskToIssue projects a Bitrix task onto an IssueDraft. portalOrigin is
// passed through to BBCodeToMarkdown so portal-internal links become absolute.
// Title falls back to
// a stable placeholder so an issue is never created with an empty title (which
// the create path rejects).
func MapTaskToIssue(task *Task, portalOrigin string) IssueDraft {
	title := strings.TrimSpace(task.Title)
	if title == "" {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			title = "Bitrix task"
		} else {
			title = "Bitrix task " + id
		}
	}
	return IssueDraft{
		Title:       title,
		Description: BBCodeToMarkdown(task.Description, portalOrigin),
		Status:      MapStatus(task.Status),
	}
}

// RouteConfig describes how a Bitrix task is routed to a Agora workspace.
//
//   - DefaultSlug: the catch-all workspace slug (may be "").
//   - GroupMap:    Bitrix GROUP_ID -> workspace slug.
//   - TagSlugs:    the set of workspace slugs a task may name directly via a
//     tag. A tag whose value is in this set forces that workspace, taking
//     precedence over GROUP_ID.
type RouteConfig struct {
	DefaultSlug string
	GroupMap    map[string]string
	TagSlugs    map[string]bool
}

// ResolveWorkspaceSlug picks the destination workspace slug for a task using a
// fixed precedence:
//
//  1. An explicit tag naming a workspace slug present in cfg.TagSlugs.
//  2. The task's GROUP_ID found in cfg.GroupMap.
//  3. cfg.DefaultSlug.
//
// Returns "" when nothing resolves (caller skips the task). Tag matching is
// case-insensitive on the tag value but compares against the slugs as stored
// in TagSlugs (slugs are lowercase by convention).
func ResolveWorkspaceSlug(task *Task, cfg RouteConfig) string {
	if task != nil && len(cfg.TagSlugs) > 0 {
		for _, raw := range task.Tags {
			tag := strings.ToLower(strings.TrimSpace(raw))
			if tag == "" {
				continue
			}
			if cfg.TagSlugs[tag] {
				return tag
			}
		}
	}

	if task != nil && len(cfg.GroupMap) > 0 {
		if gid := strings.TrimSpace(task.GroupID); gid != "" {
			if slug, ok := cfg.GroupMap[gid]; ok && slug != "" {
				return slug
			}
		}
	}

	return cfg.DefaultSlug
}

// WithinRecencyWindow reports whether a task counts as current work: created OR
// last changed within `days` of `now`.
//
// Both dates matter. Created-only would drop a task opened two months ago that
// the team is actively commenting on today; changed-only would be equivalent in
// practice but reads as an accident. A window is used instead of a sprint-group
// allowlist because sprint groups are created continuously (Спринт 12, 13, 14 …),
// so any fixed list is stale within days and silently stops importing the sprint
// the team just moved to.
//
// days <= 0 disables the filter (everything is in scope). A task whose timestamps
// are BOTH unparseable is treated as in-scope: the dates are only present when the
// caller SELECTed them, and failing closed there would silently stop importing
// everything.
func WithinRecencyWindow(task *Task, days int, now time.Time) bool {
	if days <= 0 || task == nil {
		return true
	}
	cutoff := now.AddDate(0, 0, -days)
	created, okCreated := ParseTime(task.CreatedAt)
	changed, okChanged := ParseTime(task.ChangedAt)
	if !okCreated && !okChanged {
		return true
	}
	if okCreated && !created.Before(cutoff) {
		return true
	}
	if okChanged && !changed.Before(cutoff) {
		return true
	}
	return false
}
