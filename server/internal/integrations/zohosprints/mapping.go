package zohosprints

import (
	"strings"
	"time"
)

// Agora issue statuses (the values this mapping emits).
const (
	StatusBacklog    = "backlog"
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusInReview   = "in_review"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// MapStatus maps a Zoho Sprints work-item status to a Agora issue status. The
// status NAME wins (case-insensitive substring), with Zoho's "To do"/"Doing"/
// "Done" bucket (statusDescription) as the fallback for custom names. Anything
// unrecognized defaults to "todo" so an item is never dropped. The Octane RnD
// workflow uses: Open, In progress, In Review, Completed, Cancelled, Closed,
// Backlog, To be Tested, Reopen, Recurring, Fantasy / Ideas.
func MapStatus(name, bucket string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "":
		// fall through to bucket
	case strings.Contains(n, "cancel"), strings.Contains(n, "reject"), strings.Contains(n, "won't"), strings.Contains(n, "wont"):
		return StatusCancelled
	case strings.Contains(n, "backlog"), strings.Contains(n, "defer"), strings.Contains(n, "hold"),
		strings.Contains(n, "idea"), strings.Contains(n, "fantasy"), strings.Contains(n, "recurring"):
		return StatusBacklog
	case strings.Contains(n, "review"), strings.Contains(n, "test"), strings.Contains(n, "qa"), strings.Contains(n, "verify"):
		return StatusInReview
	case strings.Contains(n, "progress"), strings.Contains(n, "doing"), strings.Contains(n, "wip"), strings.Contains(n, "active"):
		return StatusInProgress
	case strings.Contains(n, "done"), strings.Contains(n, "complete"), strings.Contains(n, "closed"), strings.Contains(n, "resolved"), strings.Contains(n, "finish"):
		return StatusDone
	case strings.Contains(n, "reopen"), strings.Contains(n, "open"), strings.Contains(n, "to do"), strings.Contains(n, "todo"), strings.Contains(n, "new"):
		return StatusTodo
	}
	switch strings.ToLower(strings.TrimSpace(bucket)) {
	case "done":
		return StatusDone
	case "doing":
		return StatusInProgress
	case "to do", "todo":
		return StatusTodo
	}
	return StatusTodo
}

// IssueDraft is the transport-agnostic shape of the issue a Sprints item maps to.
type IssueDraft struct {
	Title       string
	Description string
	Status      string
}

// MapItemToIssue projects a Sprints item onto an IssueDraft, resolving the status
// id against the project's status map. Title falls back to a stable placeholder.
func MapItemToIssue(item *Item, statuses map[string]ItemStatus) IssueDraft {
	title := strings.TrimSpace(item.Name)
	if title == "" {
		if id := strings.TrimSpace(item.ID); id != "" {
			title = "Zoho Sprints item " + id
		} else {
			title = "Zoho Sprints item"
		}
	}
	var name, bucket string
	if st, ok := statuses[item.StatusID]; ok {
		name, bucket = st.Name, st.Bucket
	}
	return IssueDraft{
		Title:       title,
		Description: strings.TrimSpace(item.Desc),
		Status:      MapStatus(name, bucket),
	}
}

// ParseZohoDate parses a Zoho Sprints ISO timestamp ("2025-11-17T05:00:00.000Z").
// Returns ok=false for "", "-1", or an unparseable value (Zoho uses "-1" for an
// unset date).
func ParseZohoDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-1" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000Z07:00", "2006-01-02T15:04:05Z07:00", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// IsParentRef reports whether a parentItem value denotes a real parent item.
// Zoho uses "", "-1" or "0" for "no parent".
func IsParentRef(parentID string) bool {
	p := strings.TrimSpace(parentID)
	return p != "" && p != "-1" && p != "0"
}
