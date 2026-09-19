// Canonical shape — the vocabulary every source adapter normalizes into and
// the only vocabulary the applier knows (docs/importers-plan.md §3.2).
//
// It is deliberately SMALLER than any source. That is the point: the seam
// between "the adapter knows the vendor exists" and "the framework knows the
// database exists" only holds if the thing crossing it is fixed. Anything the
// shape cannot hold is either counted in Truncated / Warnings or preserved
// verbatim in Raw — never silently lost, because a report that says "240
// issues" when the token could only see 190 is the failure mode that destroys
// trust in the whole product.
//
// Two rules encoded here rather than left to each adapter:
//
//   - Priority is a canonical 0..4 ladder (none, urgent, high, medium, low),
//     which is Linear's own integer meaning verbatim and a straight ordinal
//     map for everyone else. There is no configuration for it by default.
//   - Relations degrade. issue_dependency.type admits four values after
//     migration 207 (blocks, blocked_by, related, duplicate); Linear's
//     `similar` and Jira's installation-defined link types downgrade to
//     `related` with the source's own type name preserved on the linkage blob.
//     Enum drift downgrades, it never crashes.
package imports

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Agora issue statuses. Plain strings, not an enum: the canonical list lives
// in the issue table's CHECK constraint, and these are the values the mapping
// is allowed to emit. Same posture as bitrix.Status* / zohoprojects.Status*.
const (
	StatusBacklog    = "backlog"
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusInReview   = "in_review"
	StatusDone       = "done"
	StatusBlocked    = "blocked"
	StatusCancelled  = "cancelled"
)

// ValidStatus reports whether s is a status the issue table will accept. The
// applier checks mapped values through this before a write so a bad override
// in workspace.settings becomes a reported warning rather than a 23514 that
// kills the run.
func ValidStatus(s string) bool {
	switch s {
	case StatusBacklog, StatusTodo, StatusInProgress, StatusInReview,
		StatusDone, StatusBlocked, StatusCancelled:
		return true
	}
	return false
}

// Agora issue priorities, as the issue table spells them.
const (
	PriorityNameNone   = "none"
	PriorityNameUrgent = "urgent"
	PriorityNameHigh   = "high"
	PriorityNameMedium = "medium"
	PriorityNameLow    = "low"
)

// Priority is the canonical 0..4 ladder. The numbering is Linear's documented
// meaning ("0 = No priority, 1 = Urgent, 2 = High, 3 = Medium, 4 = Low") because
// adopting it makes the most common import a bijection with no configuration;
// every other source maps its own ladder onto these five ordinals.
type Priority int

const (
	PriorityNone Priority = iota
	PriorityUrgent
	PriorityHigh
	PriorityMedium
	PriorityLow
)

// Name renders a canonical priority as the string the issue table stores.
// Out-of-range input lands on "none" rather than failing the write — a source
// that grows a sixth priority must not be able to drop an issue.
func (p Priority) Name() string {
	switch p {
	case PriorityUrgent:
		return PriorityNameUrgent
	case PriorityHigh:
		return PriorityNameHigh
	case PriorityMedium:
		return PriorityNameMedium
	case PriorityLow:
		return PriorityNameLow
	case PriorityNone:
		return PriorityNameNone
	}
	return PriorityNameNone
}

// Known reports whether p is one of the five ladder positions. The plan uses
// it to warn about a source value the adapter could not place, which is a
// different thing from "the issue had no priority".
func (p Priority) Known() bool { return p >= PriorityNone && p <= PriorityLow }

// Source identifies the tracker a bundle came from. Kind is the value stored
// in import_connection.source / import_job.source and in every external_ref;
// Ref is the human-facing workspace or site identifier, for display only.
type Source struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
}

// Known source kinds. The column is free-form text on purpose (a new source
// must not need a migration), so these are the names the code agrees on, not a
// constraint.
const (
	SourceLinear = "linear"
	SourceJira   = "jira"
)

// User is every actor referenced anywhere in the bundle — assignees, creators,
// comment authors. The adapter fills it once; the identity resolver decides
// what each one becomes in Agora (§3.3).
type User struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	AvatarURL  string `json:"avatar_url,omitempty"`
	Active     bool   `json:"active"`
	// Bot reports a non-human actor (Linear's integration users). They resolve
	// to the import identity rather than being provisioned as members.
	Bot bool `json:"bot,omitempty"`
}

// Container is whatever the source calls the thing issues live in: a Linear
// team or project, a Jira project, a Trello board. One container becomes one
// Agora project.
type Container struct {
	ExternalID  string `json:"external_id"`
	Key         string `json:"key,omitempty"` // "ENG" — the identifier prefix, when the source has one
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
}

// Iteration is a Linear cycle or a Jira sprint. Becomes an Agora sprint.
type Iteration struct {
	ExternalID  string     `json:"external_id"`
	ContainerID string     `json:"container_id,omitempty"`
	Name        string     `json:"name"`
	Goal        string     `json:"goal,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	Completed   bool       `json:"completed,omitempty"`
}

// State is one workflow state, carrying the SOURCE'S OWN CATEGORY. The category
// is the load-bearing field: categories are stable across installs and names
// are not, so the status mapping reads the category first and the name second
// (§3.4).
type State struct {
	ExternalID  string  `json:"external_id"`
	ContainerID string  `json:"container_id,omitempty"`
	Name        string  `json:"name"`
	Category    string  `json:"category"` // Linear WorkflowState.type, Jira statusCategory
	Position    float64 `json:"position,omitempty"`
}

// Label is a source label. Name-matched to issue_label case-insensitively and
// created when absent; the source colour rides along when there is one.
type Label struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Color      string `json:"color,omitempty"`
}

// Comment is one message on an issue. Body is already Markdown — conversion
// (ADF, BBCode) is the adapter's job, not the framework's.
type Comment struct {
	ExternalID   string    `json:"external_id"`
	AuthorID     string    `json:"author_id,omitempty"`
	BodyMarkdown string    `json:"body_markdown"`
	URL          string    `json:"url,omitempty"`
	ParentID     string    `json:"parent_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Attachment is a file on an issue. URL is fetched with the SOURCE'S auth,
// server-side — handing a signed URL to a browser is a leak (§3.6).
type Attachment struct {
	ExternalID  string    `json:"external_id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	ContentType string    `json:"content_type,omitempty"`
	Size        int64     `json:"size,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// RelationKind is the vocabulary issue_dependency.type admits after migration
// 207. Anything a source has beyond these four degrades to RelationRelated.
type RelationKind string

const (
	RelationBlocks    RelationKind = "blocks"
	RelationBlockedBy RelationKind = "blocked_by"
	RelationRelated   RelationKind = "related"
	RelationDuplicate RelationKind = "duplicate"
)

// Relation is a typed edge between two source issues, resolved to Agora issue
// ids in the applier's last pass (a blocks-link can point at an issue that only
// exists after the pass that creates it).
type Relation struct {
	ExternalID       string       `json:"external_id,omitempty"`
	Kind             RelationKind `json:"kind"`
	TargetExternalID string       `json:"target_external_id"`
	// SourceType is the source's own name for this relation, preserved
	// verbatim when Kind is a downgrade ("similar", "Cloners", "Causes").
	SourceType string `json:"source_type,omitempty"`
}

// DegradeRelation maps a source's relation name onto the four values the CHECK
// admits, reporting whether the mapping was exact. An unknown name is NOT an
// error: it becomes `related` and the caller keeps the original on the edge.
func DegradeRelation(sourceType string) (RelationKind, bool) {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "blocks":
		return RelationBlocks, true
	case "blocked_by", "blocked by", "is blocked by", "blockedby":
		return RelationBlockedBy, true
	case "duplicate", "duplicates", "is duplicated by", "duplicate_of":
		return RelationDuplicate, true
	case "related", "relates", "relates to", "similar":
		// `similar` is Linear's fourth value and has no Agora equivalent; it
		// lands on `related` and keeps its name on the edge.
		return RelationRelated, strings.EqualFold(strings.TrimSpace(sourceType), "related")
	}
	return RelationRelated, false
}

// Issue is the canonical issue. Every *ID field holds a SOURCE id and is
// resolved against the bundle's own tables, never against the database — the
// adapter has no database.
type Issue struct {
	ExternalID   string   `json:"external_id"` // the upsert key
	Identifier   string   `json:"identifier"`  // human key: "ENG-142"
	URL          string   `json:"url,omitempty"`
	Title        string   `json:"title"`
	BodyMarkdown string   `json:"body_markdown,omitempty"`
	StateID      string   `json:"state_id,omitempty"`
	Priority     Priority `json:"priority"`
	CreatorID    string   `json:"creator_id,omitempty"`
	AssigneeID   string   `json:"assignee_id,omitempty"`
	LabelIDs     []string `json:"label_ids,omitempty"`
	ContainerID  string   `json:"container_id,omitempty"`
	IterationID  string   `json:"iteration_id,omitempty"`
	ParentID     string   `json:"parent_id,omitempty"`

	Estimate    *float64   `json:"estimate,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	Archived    bool       `json:"archived,omitempty"`

	Comments    []Comment    `json:"comments,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Relations   []Relation   `json:"relations,omitempty"`

	// Raw holds everything the canonical shape could not carry. It exists so a
	// field the shape does not model yet is recoverable from the row rather
	// than requiring a second full import.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Bundle is one adapter's complete answer for one scope.
type Bundle struct {
	Source     Source      `json:"source"`
	Users      []User      `json:"users,omitempty"`
	Containers []Container `json:"containers,omitempty"`
	Iterations []Iteration `json:"iterations,omitempty"`
	States     []State     `json:"states,omitempty"`
	Labels     []Label     `json:"labels,omitempty"`
	Issues     []Issue     `json:"issues,omitempty"`

	// Truncated is load-bearing, not diagnostics: entity kind -> how many rows
	// the adapter knows it did NOT fetch. Every adapter is REQUIRED to count
	// and name what it could not reach.
	Truncated map[string]int `json:"truncated,omitempty"`
	// Warnings carries the reasons, in the operator's language, including the
	// reason any count above is an estimate rather than exact.
	Warnings []string `json:"warnings,omitempty"`
}

// AddTruncated records that n rows of a kind were not fetched, and why. Called
// by the adapter at the moment it decides to stop, so the reason is the real
// one rather than a guess assembled later.
func (b *Bundle) AddTruncated(kind string, n int, reason string) {
	if n <= 0 {
		return
	}
	if b.Truncated == nil {
		b.Truncated = map[string]int{}
	}
	b.Truncated[kind] += n
	if reason != "" {
		b.Warn("%s: %d %s not fetched (%s)", kind, n, kind, reason)
	}
}

// Warn appends a formatted operator-facing warning, deduplicated so a
// per-issue condition does not print ten thousand times.
func (b *Bundle) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, existing := range b.Warnings {
		if existing == msg {
			return
		}
	}
	b.Warnings = append(b.Warnings, msg)
}

// UserByID returns the bundle's record for a source user id.
func (b *Bundle) UserByID(id string) (User, bool) {
	for i := range b.Users {
		if b.Users[i].ExternalID == id {
			return b.Users[i], true
		}
	}
	return User{}, false
}

// StateByID returns the bundle's record for a source workflow state id.
func (b *Bundle) StateByID(id string) (State, bool) {
	for i := range b.States {
		if b.States[i].ExternalID == id {
			return b.States[i], true
		}
	}
	return State{}, false
}

// LabelByID returns the bundle's record for a source label id.
func (b *Bundle) LabelByID(id string) (Label, bool) {
	for i := range b.Labels {
		if b.Labels[i].ExternalID == id {
			return b.Labels[i], true
		}
	}
	return Label{}, false
}

// ContainerByID returns the bundle's record for a source container id.
func (b *Bundle) ContainerByID(id string) (Container, bool) {
	for i := range b.Containers {
		if b.Containers[i].ExternalID == id {
			return b.Containers[i], true
		}
	}
	return Container{}, false
}

// IterationByID returns the bundle's record for a source iteration id.
func (b *Bundle) IterationByID(id string) (Iteration, bool) {
	for i := range b.Iterations {
		if b.Iterations[i].ExternalID == id {
			return b.Iterations[i], true
		}
	}
	return Iteration{}, false
}

// ExternalRef is the linkage blob written on every imported row — one shape on
// both entities, replacing the per-vendor column (§3.5). It is what makes the
// SECOND run an update: issues match on source+id out of issue.metadata,
// comments on the comment.external_source/external_id column pair.
type ExternalRef struct {
	Source     string `json:"source"`
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	URL        string `json:"url,omitempty"`
	// Author preserves the real name when the author could not be resolved to
	// an Agora member, so provenance survives attribution to the import
	// identity.
	Author     string `json:"author,omitempty"`
	ImportedAt string `json:"imported_at,omitempty"`
	ImportID   string `json:"import_id,omitempty"`
	// SourceType preserves the source's own relation/type name where the
	// canonical vocabulary had to downgrade.
	SourceType string `json:"source_type,omitempty"`
}

// ExternalRefKey is the metadata key the issue-side linkage lives under. It
// must match the expression index built in migration 207
// (idx_issue_external_ref) or the "have I already imported this?" lookup stops
// being an index scan.
const ExternalRefKey = "external_ref"

// MergeExternalRef writes ref into an existing issue.metadata blob under
// ExternalRefKey, preserving every other key. A nil/empty/invalid existing blob
// is treated as an empty object rather than an error: metadata is user-facing
// JSONB and an import must not fail because something else put junk in it.
func MergeExternalRef(existing []byte, ref ExternalRef) ([]byte, error) {
	meta := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &meta); err != nil {
			meta = map[string]json.RawMessage{}
		}
	}
	blob, err := json.Marshal(ref)
	if err != nil {
		return nil, fmt.Errorf("imports: marshal external_ref: %w", err)
	}
	meta[ExternalRefKey] = blob
	return json.Marshal(meta)
}

// ReadExternalRef pulls the linkage blob back out of a metadata document,
// reporting ok=false when there is none. Tolerant of a malformed blob for the
// same reason MergeExternalRef is.
func ReadExternalRef(metadata []byte) (ExternalRef, bool) {
	if len(metadata) == 0 {
		return ExternalRef{}, false
	}
	var meta struct {
		Ref *ExternalRef `json:"external_ref"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil || meta.Ref == nil {
		return ExternalRef{}, false
	}
	return *meta.Ref, meta.Ref.ID != ""
}
