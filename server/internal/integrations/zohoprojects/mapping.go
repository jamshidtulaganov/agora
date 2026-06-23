package zohoprojects

import "strings"

// Agora issue statuses. Kept as plain strings (not an enum) because the
// canonical list lives in the DB / handler layer; these are the values the
// mapping emits. Mirrors bitrix.Status*.
const (
	StatusBacklog    = "backlog"
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusInReview   = "in_review"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// MapStatus maps a Zoho Projects task status to a Agora issue status.
//
// Zoho Projects has no fixed numeric status codes the way Bitrix does — a
// portal defines its own named statuses, each bucketed by Zoho into a "type"
// of either "open" or "closed". The mapping therefore works on the human status
// NAME (case-insensitive, substring-matched for the common defaults), and falls
// back to the status TYPE so a custom status still lands somewhere sensible:
//
//	name contains "backlog" / "defer"            -> backlog
//	name contains "progress" / "active" / "wip"  -> in_progress
//	name contains "review" / "testing" / "qa"    -> in_review
//	name contains "cancel" / "reject" / "won't"  -> cancelled
//	name contains "done" / "complete" / "closed" -> done
//	name contains "open" / "new" / "todo" / "to do" -> todo
//
// Fallback by Zoho status "type":
//	type == "closed" -> done
//	type == "open"   -> todo
//
// Anything still unrecognized (including empty) defaults to "todo" so a task is
// always actionable rather than dropped. Mirrors bitrix.MapStatus's
// "never drop" contract.
func MapStatus(status string) string {
	return mapStatus(status, "")
}

// MapStatusWithType is MapStatus with an explicit Zoho status "type" fallback
// (open/closed) for custom status names the name-matcher doesn't recognize.
func MapStatusWithType(name, statusType string) string {
	return mapStatus(name, statusType)
}

func mapStatus(name, statusType string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "":
		// no name — fall through to type below
	case strings.Contains(n, "backlog"), strings.Contains(n, "defer"), strings.Contains(n, "hold"):
		return StatusBacklog
	case strings.Contains(n, "cancel"), strings.Contains(n, "reject"), strings.Contains(n, "won't"), strings.Contains(n, "wont"):
		return StatusCancelled
	case strings.Contains(n, "review"), strings.Contains(n, "testing"), strings.Contains(n, "qa"), strings.Contains(n, "verify"):
		return StatusInReview
	case strings.Contains(n, "progress"), strings.Contains(n, "active"), strings.Contains(n, "wip"), strings.Contains(n, "doing"), strings.Contains(n, "started"):
		return StatusInProgress
	case strings.Contains(n, "done"), strings.Contains(n, "complete"), strings.Contains(n, "closed"), strings.Contains(n, "resolved"), strings.Contains(n, "finish"):
		return StatusDone
	case strings.Contains(n, "to do"), strings.Contains(n, "todo"), strings.Contains(n, "open"), strings.Contains(n, "new"):
		return StatusTodo
	}
	switch strings.ToLower(strings.TrimSpace(statusType)) {
	case "closed":
		return StatusDone
	case "open":
		return StatusTodo
	}
	return StatusTodo
}

// ZohoStatusNameFromIssue is the inverse of MapStatus: it maps a Agora issue
// status to a canonical Zoho Projects status NAME, used as a hint when mirroring
// a status change back to a task. Because a Zoho portal defines its own status
// names per project, this canonical name is only a fallback for logging —
// ResolveCustomStatusID picks the actual per-project status id by re-bucketing
// the project's real statuses. The names below match Zoho's out-of-the-box
// defaults:
//
//	backlog     -> "Deferred"
//	todo        -> "Open"
//	in_progress -> "In Progress"
//	in_review   -> "In Review"
//	done        -> "Closed"
//	cancelled   -> "Cancelled"
//
// Unknown statuses default to "Open", matching MapStatus's default landing zone.
func ZohoStatusNameFromIssue(issueStatus string) string {
	switch strings.TrimSpace(issueStatus) {
	case StatusBacklog:
		return "Deferred"
	case StatusTodo:
		return "Open"
	case StatusInProgress:
		return "In Progress"
	case StatusInReview:
		return "In Review"
	case StatusDone:
		return "Closed"
	case StatusCancelled:
		return "Cancelled"
	default:
		return "Open"
	}
}

// ResolveCustomStatusID picks the project custom-status id that best represents
// a Agora issue status, for the outbound status push. It re-uses the SAME
// name+type bucketing as the inbound MapStatus so the round-trip is symmetric:
// every one of the project's real statuses is mapped to its Agora bucket, and
// the first whose bucket equals the target wins. Returns ok=false when no status
// maps to the target (the caller then skips the push rather than guessing a
// wrong status). For the terminal/initial buckets (done/todo) a second pass also
// accepts a Zoho status "type" match (closed/open) so a portal with oddly-named
// terminal statuses still resolves.
func ResolveCustomStatusID(statuses []CustomStatus, agoraStatus string) (string, bool) {
	target := strings.TrimSpace(agoraStatus)
	if target == "" {
		return "", false
	}
	// First pass: exact bucket match by name+type (symmetric with MapStatus).
	for _, s := range statuses {
		if mapStatus(s.Name, s.Type) == target {
			return s.ID, true
		}
	}
	// Second pass for the terminal/initial buckets: fall back to the Zoho status
	// "type" so done -> any closed status and todo -> any open status even when
	// the name-matcher recognized none of the project's statuses as that bucket.
	switch target {
	case StatusDone:
		for _, s := range statuses {
			if strings.EqualFold(strings.TrimSpace(s.Type), "closed") {
				return s.ID, true
			}
		}
	case StatusTodo:
		for _, s := range statuses {
			if strings.EqualFold(strings.TrimSpace(s.Type), "open") {
				return s.ID, true
			}
		}
	}
	return "", false
}

// IssueDraft is the transport-agnostic shape of the issue a Zoho task maps to.
// The handler turns it into service.IssueCreateParams. Mirrors bitrix.IssueDraft.
type IssueDraft struct {
	Title       string
	Description string
	Status      string
}

// MapTaskToIssue projects a Zoho task onto an IssueDraft. Title falls back to a
// stable placeholder so an issue is never created with an empty title (which the
// create path rejects). The status uses the name first, then the Zoho status
// "type" as a fallback.
func MapTaskToIssue(task *Task) IssueDraft {
	title := strings.TrimSpace(task.Name)
	if title == "" {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			title = "Zoho task"
		} else {
			title = "Zoho task " + id
		}
	}
	return IssueDraft{
		Title:       title,
		Description: strings.TrimSpace(task.Description),
		Status:      MapStatusWithType(task.Status, task.StatusType),
	}
}

// TasklistIsSprint reports whether a Zoho task-list name denotes a sprint (e.g.
// "Sprint 7", "Спринт 12"). Case-insensitive, matching either the Latin
// "sprint" or the Cyrillic "спринт" anywhere in the name. An empty name is not a
// sprint. Mirrors bitrixGroupIsSprint so the two importers treat sprint-named
// containers identically.
func TasklistIsSprint(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	return strings.Contains(n, "sprint") || strings.Contains(n, "спринт")
}
