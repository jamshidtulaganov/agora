package bitrix

import "strings"

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

// BitrixStatusFromIssue is the inverse of MapStatus: it maps a Agora issue
// status to the Bitrix status code used when mirroring a status change back to
// the portal. It is intentionally NOT a perfect round-trip (Bitrix has more
// codes than Agora statuses) — it picks the most faithful Bitrix code for
// each Agora status:
//
//	backlog     -> 6 (deferred)
//	todo        -> 2 (pending)
//	in_progress -> 3 (in progress)
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
	case StatusInProgress:
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

// MapTaskToIssue projects a Bitrix task onto an IssueDraft. Title falls back to
// a stable placeholder so an issue is never created with an empty title (which
// the create path rejects).
func MapTaskToIssue(task *Task) IssueDraft {
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
		Description: task.Description,
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
