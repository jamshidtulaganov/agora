// The adapter seam (docs/importers-plan.md §3.1).
//
// The pipeline is fetch → normalize → map → dry-run diff → apply → link back,
// and the first two stages are the only ones that know a vendor exists. The
// rule that makes that worth having: THE ADAPTER NEVER WRITES TO THE DATABASE.
// It returns canonical structs and nothing else.
//
// Two things follow. A recorded fixture is a complete adapter test — no DB, no
// network, the whole contract in one httptest.Server. And the multi-tenancy
// rules stay in one place, because every write goes through the framework's
// applier, which takes a workspace_id and filters by it exactly as every other
// query does.
package imports

import (
	"context"
	"io"
	"time"
)

// Scope is what the operator chose to import: which containers, how far back,
// and the options that change the size of the job. It is stored verbatim in
// import_job.scope and replayed on a re-run, so a field added here must be
// optional — an old job row must still decode.
type Scope struct {
	// Containers lists source container ids (or keys) to import. Empty means
	// "everything the credential can see", which the dry run reports back
	// before anyone confirms it.
	Containers []string `json:"containers,omitempty"`
	// Since bounds the walk to issues updated at or after this instant. Nil
	// imports the full history — the normal case for a migration.
	Since *time.Time `json:"since,omitempty"`
	// IncludeArchived pulls the source's archived/closed-and-hidden issues.
	// Off by default: most teams do not want a two-year archive on day one.
	IncludeArchived bool `json:"include_archived,omitempty"`
	// IncludeComments and IncludeAttachments are on for a migration and can be
	// turned off for a fast structural preview. A tracker without its
	// discussion is a paste, so the default for both is true — see
	// (Scope).withDefaults.
	IncludeComments    *bool `json:"include_comments,omitempty"`
	IncludeAttachments *bool `json:"include_attachments,omitempty"`
	// ProvisionUsers creates an Agora user + member for an unmatched source
	// user. OFF by default: silently growing the member roster during an
	// import is a billing surprise and a security surprise at once (§3.3).
	ProvisionUsers bool `json:"provision_users,omitempty"`
	// MaxIssues caps the walk. 0 means uncapped; when a cap truncates the
	// bundle the adapter records it in Bundle.Truncated rather than reporting
	// a number it did not reach.
	MaxIssues int `json:"max_issues,omitempty"`
}

// WantsComments / WantsAttachments apply the defaults a migration needs. They
// exist because the zero value of a *bool in a decoded old job row is nil, and
// nil must mean "yes" here, not "no".
func (s Scope) WantsComments() bool {
	return s.IncludeComments == nil || *s.IncludeComments
}

func (s Scope) WantsAttachments() bool {
	return s.IncludeAttachments == nil || *s.IncludeAttachments
}

// ProbeResult is what a credential check learned. Status is the value stored in
// import_connection.probe_status: ok | invalid | unreachable. The distinction
// matters to the operator — "your key is wrong" and "Linear is down" have
// different next actions.
type ProbeResult struct {
	Status  string `json:"status"`
	Account string `json:"account,omitempty"` // who the token belongs to, for confirmation
	Ref     string `json:"ref,omitempty"`     // the source workspace/site it reaches
	Detail  string `json:"detail,omitempty"`  // operator-facing reason when not ok
}

const (
	ProbeOK          = "ok"
	ProbeInvalid     = "invalid"
	ProbeUnreachable = "unreachable"
)

// Progress is one throttled tick from a long walk or apply. The framework
// publishes it as `import:progress` scoped to the workspace; the adapter emits
// the fetch half so a 40-minute Linear walk is not a blank screen.
type Progress struct {
	Phase   string `json:"phase"` // fetch | plan | apply
	Kind    string `json:"kind"`  // issues | comments | attachments | relations | …
	Done    int    `json:"done"`
	Total   int    `json:"total,omitempty"` // 0 when the source cannot produce an exact total
	Message string `json:"message,omitempty"`
}

// ProgressFunc receives ticks. Always safe to call — the framework passes a
// no-op rather than nil, so no adapter needs a nil check.
type ProgressFunc func(Progress)

// NopProgress is the ProgressFunc used when nobody is listening.
func NopProgress(Progress) {}

// Adapter is everything a source must provide. Five methods, and none of them
// touch the database.
//
// The split between Containers and Fetch is deliberate: the scope picker needs
// a cheap "what is in there?" answer long before anyone is willing to pay for a
// full walk, and on a complexity-metered API (Linear) those are very different
// requests.
type Adapter interface {
	// Source names the vendor. Kind is the value written to
	// import_job.source and to every external_ref.
	Source() Source

	// Defaults are this source's mapping defaults — the status table keyed by
	// the source's own category and name, and the priority ladder. The
	// framework layers the per-workspace overrides on top (§3.4).
	Defaults() Defaults

	// Probe verifies the credential without importing anything. It must not
	// return the token in any field of the result or the error.
	Probe(ctx context.Context) (ProbeResult, error)

	// Containers lists what could be imported, for the scope picker. Cheap by
	// contract: no issues, no comments.
	Containers(ctx context.Context) ([]Container, error)

	// Fetch walks the source for scope and returns the canonical bundle. It
	// must honour ctx cancellation between pages, and it must record in
	// Bundle.Truncated anything it could not reach — a rate limit it gave up
	// on, a container the token cannot see, a cap in Scope.
	Fetch(ctx context.Context, scope Scope, progress ProgressFunc) (*Bundle, error)
}

// AttachmentOpener is the optional half of an adapter: streaming a file out of
// the source with the source's own auth, server-side. An adapter that does not
// implement it simply has its attachments reported as skipped with a reason
// rather than silently dropped.
//
// The contract is a stream, not bytes: a 200 MB screen recording must not be
// buffered to satisfy an interface.
type AttachmentOpener interface {
	OpenAttachment(ctx context.Context, att Attachment) (io.ReadCloser, error)
}

// AttachmentSink is the framework's other side of the same seam: where an
// opened file goes. It is an interface because packages/imports must not depend
// on the storage layer — the handler that owns Storage supplies this in PR 4.
//
// Returning ("", nil) is not allowed; a sink either stores the file and names
// it, or errors.
type AttachmentSink interface {
	StoreAttachment(ctx context.Context, issueID string, att Attachment, body io.Reader) error
}
