// Package importmem is an in-memory implementation of the importer framework's
// three seams — imports.Store, imports.JobStore and imports.IdentityStore.
//
// It exists so the contract that matters most can be tested without a database:
// running the same import twice produces "created: 0, updated: N". That
// property lives in the interaction between FindIssueByExternalRef, the
// external_ref blob, and the comment unique index — not in any single
// statement — so a unit test of the applier is the only place it can be pinned
// cheaply, and the adapter tests (which must never touch a database) need the
// same fake to prove a recorded fixture applies idempotently.
//
// Where a behaviour of the real schema is load-bearing, it is reproduced here
// rather than approximated, and the comment says which one:
//
//   - IncrementIssueCounter takes GREATEST(counter+1, max(number)+1), the
//     self-heal a bulk load that preserved external numbering paid for.
//   - UpsertCommentImported dedupes on (issue_id, external_source, external_id),
//     which is uq_comment_external_ref.
//   - UpsertIssueDependency reports 0 rows affected for an edge that already
//     exists, which is the NOT EXISTS guard.
//   - Reads that find nothing return pgx.ErrNoRows, because that is what the
//     applier branches on.
//
// This package is imported only by tests. It is not wired into the server.
package importmem

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Store is the fake. Every field is exported so a test can assert against the
// rows directly instead of through a query it would also have to trust.
type Store struct {
	mu sync.Mutex

	Projects     []db.Project
	Sprints      []db.Sprint
	Labels       []db.IssueLabel
	Issues       []db.Issue
	Comments     []db.Comment
	Dependencies []db.IssueDependency
	Jobs         []db.ImportJob

	IssueLabels map[string][]string // issue id -> label ids
	IssueSprint map[string]string   // issue id -> sprint id

	Users      []db.User
	Members    map[string]map[string]string // workspace id -> user id -> role
	Identities map[string]string            // "provider/external id" -> user id

	// IssueCounter mirrors workspace.issue_counter.
	IssueCounter int32

	// Calls counts writes by name, so a test can assert that a re-run did not
	// simply skip the work it claims to have updated.
	Calls map[string]int

	seq int
}

// New builds an empty store.
func New() *Store {
	return &Store{
		IssueLabels: map[string][]string{},
		IssueSprint: map[string]string{},
		Members:     map[string]map[string]string{},
		Identities:  map[string]string{},
		Calls:       map[string]int{},
	}
}

// nextID hands out deterministic uuids so a failing test prints a stable id.
func (s *Store) nextID() pgtype.UUID {
	s.seq++
	return util.MustParseUUID(fmt.Sprintf("00000000-0000-4000-8000-%012d", s.seq))
}

func (s *Store) note(name string) { s.Calls[name]++ }

func key(u pgtype.UUID) string { return util.UUIDToString(u) }

// --- issue numbering --------------------------------------------------------

// IncrementIssueCounter reproduces the GREATEST self-heal: the naive counter+1
// and (max existing number)+1, whichever is larger. Without it a lagging
// counter hands out a number that already exists and every create fails on
// uq_issue_workspace_number (sd-main hit exactly this: counter 179, max 319).
func (s *Store) IncrementIssueCounter(_ context.Context, workspaceID pgtype.UUID) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("IncrementIssueCounter")

	next := s.IssueCounter + 1
	var maxNumber int32
	for _, issue := range s.Issues {
		if key(issue.WorkspaceID) == key(workspaceID) && issue.Number > maxNumber {
			maxNumber = issue.Number
		}
	}
	if maxNumber+1 > next {
		next = maxNumber + 1
	}
	s.IssueCounter = next
	return next, nil
}

// --- projects ---------------------------------------------------------------

func (s *Store) ListProjects(_ context.Context, arg db.ListProjectsParams) ([]db.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []db.Project{}
	for _, p := range s.Projects {
		if key(p.WorkspaceID) == key(arg.WorkspaceID) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Store) CreateProject(_ context.Context, arg db.CreateProjectParams) (db.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("CreateProject")
	p := db.Project{
		ID:          s.nextID(),
		WorkspaceID: arg.WorkspaceID,
		Title:       arg.Title,
		Description: arg.Description,
		Icon:        arg.Icon,
		Status:      arg.Status,
		Priority:    arg.Priority,
		Settings:    []byte(`{}`),
	}
	s.Projects = append(s.Projects, p)
	return p, nil
}

// SetProjectSettingKey merges one key into project.settings, which is what the
// real jsonb_set query does — a whole-document overwrite here would hide a bug
// where the importer clobbers a project's other settings.
func (s *Store) SetProjectSettingKey(_ context.Context, arg db.SetProjectSettingKeyParams) (db.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("SetProjectSettingKey")
	for i := range s.Projects {
		if key(s.Projects[i].ID) != key(arg.ID) || key(s.Projects[i].WorkspaceID) != key(arg.WorkspaceID) {
			continue
		}
		doc := map[string]json.RawMessage{}
		if len(s.Projects[i].Settings) > 0 {
			_ = json.Unmarshal(s.Projects[i].Settings, &doc)
		}
		doc[arg.Key] = append(json.RawMessage(nil), arg.Value...)
		blob, err := json.Marshal(doc)
		if err != nil {
			return db.Project{}, err
		}
		s.Projects[i].Settings = blob
		return s.Projects[i], nil
	}
	return db.Project{}, pgx.ErrNoRows
}

// --- sprints ----------------------------------------------------------------

func (s *Store) ListSprintsByProject(_ context.Context, projectID pgtype.UUID) ([]db.Sprint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []db.Sprint{}
	for _, sp := range s.Sprints {
		if key(sp.ProjectID) == key(projectID) {
			out = append(out, sp)
		}
	}
	return out, nil
}

func (s *Store) CreateSprint(_ context.Context, arg db.CreateSprintParams) (db.Sprint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("CreateSprint")
	sp := db.Sprint{
		ID:          s.nextID(),
		WorkspaceID: arg.WorkspaceID,
		ProjectID:   arg.ProjectID,
		Name:        arg.Name,
		Goal:        arg.Goal,
		Status:      arg.Status,
		StartDate:   arg.StartDate,
		EndDate:     arg.EndDate,
		Branch:      arg.Branch,
	}
	s.Sprints = append(s.Sprints, sp)
	return sp, nil
}

func (s *Store) SetIssueSprint(_ context.Context, arg db.SetIssueSprintParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("SetIssueSprint")
	s.IssueSprint[key(arg.IssueID)] = key(arg.SprintID)
	return nil
}

// --- labels -----------------------------------------------------------------

func (s *Store) ListLabels(_ context.Context, workspaceID pgtype.UUID) ([]db.IssueLabel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []db.IssueLabel{}
	for _, l := range s.Labels {
		if key(l.WorkspaceID) == key(workspaceID) {
			out = append(out, l)
		}
	}
	return out, nil
}

func (s *Store) CreateLabel(_ context.Context, arg db.CreateLabelParams) (db.IssueLabel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("CreateLabel")
	l := db.IssueLabel{
		ID:          s.nextID(),
		WorkspaceID: arg.WorkspaceID,
		Name:        arg.Name,
		Color:       arg.Color,
	}
	s.Labels = append(s.Labels, l)
	return l, nil
}

// AttachLabelToIssue is idempotent, matching the (issue_id, label_id) primary
// key on issue_to_label.
func (s *Store) AttachLabelToIssue(_ context.Context, arg db.AttachLabelToIssueParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("AttachLabelToIssue")
	issue := key(arg.IssueID)
	for _, existing := range s.IssueLabels[issue] {
		if existing == key(arg.LabelID) {
			return nil
		}
	}
	s.IssueLabels[issue] = append(s.IssueLabels[issue], key(arg.LabelID))
	return nil
}

// The three seams this fake stands in for. These assertions are the reason a
// test written against it is evidence about the real applier: the day a seam
// grows a method, this file fails the build rather than the test quietly
// covering less than it claims.
var (
	_ imports.Store         = (*Store)(nil)
	_ imports.JobStore      = (*Store)(nil)
	_ imports.IdentityStore = (*Store)(nil)
)
