// The issue/comment/relation half of the fake, where the idempotency contract
// actually lives.
package importmem

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// FindIssueByExternalRef reproduces the JSONB lookup migration 207 indexed:
// match on metadata->'external_ref'->>'source' and ->>'id' within one
// workspace. Returning pgx.ErrNoRows on a miss matters — the applier branches
// on exactly that error to decide create vs update.
func (s *Store) FindIssueByExternalRef(_ context.Context, arg db.FindIssueByExternalRefParams) (db.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, issue := range s.Issues {
		if key(issue.WorkspaceID) != key(arg.WorkspaceID) {
			continue
		}
		source, id := externalRefOf(issue.Metadata)
		if source == arg.Source && id == arg.ExternalID {
			return issue, nil
		}
	}
	return db.Issue{}, pgx.ErrNoRows
}

func (s *Store) ListIssuesByExternalSource(_ context.Context, arg db.ListIssuesByExternalSourceParams) ([]db.ListIssuesByExternalSourceRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []db.ListIssuesByExternalSourceRow{}
	for _, issue := range s.Issues {
		if key(issue.WorkspaceID) != key(arg.WorkspaceID) {
			continue
		}
		source, id := externalRefOf(issue.Metadata)
		if source != arg.Source || id == "" {
			continue
		}
		out = append(out, db.ListIssuesByExternalSourceRow{ID: issue.ID, Number: issue.Number, ExternalID: id})
	}
	return out, nil
}

func (s *Store) CreateIssueImported(_ context.Context, arg db.CreateIssueImportedParams) (db.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("CreateIssueImported")
	issue := db.Issue{
		ID:            s.nextID(),
		WorkspaceID:   arg.WorkspaceID,
		Title:         arg.Title,
		Description:   arg.Description,
		Status:        arg.Status,
		Priority:      arg.Priority,
		AssigneeType:  arg.AssigneeType,
		AssigneeID:    arg.AssigneeID,
		CreatorType:   arg.CreatorType,
		CreatorID:     arg.CreatorID,
		ParentIssueID: arg.ParentIssueID,
		Position:      arg.Position,
		StartDate:     arg.StartDate,
		DueDate:       arg.DueDate,
		Number:        arg.Number,
		ProjectID:     arg.ProjectID,
		Metadata:      arg.Metadata,
		CreatedAt:     arg.CreatedAt,
		UpdatedAt:     arg.UpdatedAt,
	}
	s.Issues = append(s.Issues, issue)
	return issue, nil
}

// UpdateIssueImported sets every column the importer owns and leaves the rest
// alone — including number and creator, because re-importing must not renumber
// an issue people have already linked to.
func (s *Store) UpdateIssueImported(_ context.Context, arg db.UpdateIssueImportedParams) (db.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("UpdateIssueImported")
	for i := range s.Issues {
		if key(s.Issues[i].ID) != key(arg.ID) || key(s.Issues[i].WorkspaceID) != key(arg.WorkspaceID) {
			continue
		}
		s.Issues[i].Title = arg.Title
		s.Issues[i].Description = arg.Description
		s.Issues[i].Status = arg.Status
		s.Issues[i].Priority = arg.Priority
		s.Issues[i].AssigneeType = arg.AssigneeType
		s.Issues[i].AssigneeID = arg.AssigneeID
		s.Issues[i].ParentIssueID = arg.ParentIssueID
		s.Issues[i].ProjectID = arg.ProjectID
		s.Issues[i].StartDate = arg.StartDate
		s.Issues[i].DueDate = arg.DueDate
		s.Issues[i].Metadata = arg.Metadata
		s.Issues[i].UpdatedAt = arg.UpdatedAt
		return s.Issues[i], nil
	}
	return db.Issue{}, pgx.ErrNoRows
}

// UpsertCommentImported is uq_comment_external_ref in Go: the partial unique
// index on (issue_id, external_source, external_id) means a re-import UPDATES
// an edited comment rather than appending a second copy. The Bitrix design
// achieved this with an unbounded synced-id ARRAY on the issue, which is the
// thing migration 207 replaced.
func (s *Store) UpsertCommentImported(_ context.Context, arg db.UpsertCommentImportedParams) (db.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("UpsertCommentImported")
	if arg.ExternalSource.Valid && arg.ExternalID.Valid {
		for i := range s.Comments {
			c := &s.Comments[i]
			if key(c.IssueID) == key(arg.IssueID) &&
				c.ExternalSource.String == arg.ExternalSource.String &&
				c.ExternalID.String == arg.ExternalID.String {
				c.Content = arg.Content
				c.UpdatedAt = arg.UpdatedAt
				return *c, nil
			}
		}
	}
	comment := db.Comment{
		ID:             s.nextID(),
		IssueID:        arg.IssueID,
		WorkspaceID:    arg.WorkspaceID,
		AuthorType:     arg.AuthorType,
		AuthorID:       arg.AuthorID,
		Content:        arg.Content,
		Type:           arg.Type,
		ParentID:       arg.ParentID,
		ExternalSource: arg.ExternalSource,
		ExternalID:     arg.ExternalID,
		CreatedAt:      arg.CreatedAt,
		UpdatedAt:      arg.UpdatedAt,
	}
	s.Comments = append(s.Comments, comment)
	return comment, nil
}

// UpsertIssueDependency returns 0 rows affected when the edge already exists,
// which is the NOT EXISTS guard's answer and the re-run's success case.
func (s *Store) UpsertIssueDependency(_ context.Context, arg db.UpsertIssueDependencyParams) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("UpsertIssueDependency")
	for _, d := range s.Dependencies {
		if key(d.IssueID) == key(arg.IssueID) &&
			key(d.DependsOnIssueID) == key(arg.DependsOnIssueID) &&
			d.Type == arg.Type {
			return 0, nil
		}
	}
	s.Dependencies = append(s.Dependencies, db.IssueDependency{
		ID:               s.nextID(),
		IssueID:          arg.IssueID,
		DependsOnIssueID: arg.DependsOnIssueID,
		Type:             arg.Type,
	})
	return 1, nil
}

// IssueByExternalID is a test convenience, not part of any interface.
func (s *Store) IssueByExternalID(source, id string) (db.Issue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, issue := range s.Issues {
		if gotSource, gotID := externalRefOf(issue.Metadata); gotSource == source && gotID == id {
			return issue, true
		}
	}
	return db.Issue{}, false
}

// CommentsForIssue is a test convenience.
func (s *Store) CommentsForIssue(issueID pgtype.UUID) []db.Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []db.Comment{}
	for _, c := range s.Comments {
		if key(c.IssueID) == key(issueID) {
			out = append(out, c)
		}
	}
	return out
}

func externalRefOf(metadata []byte) (source, id string) {
	if len(metadata) == 0 {
		return "", ""
	}
	var doc struct {
		Ref struct {
			Source string `json:"source"`
			ID     string `json:"id"`
		} `json:"external_ref"`
	}
	if err := json.Unmarshal(metadata, &doc); err != nil {
		return "", ""
	}
	return doc.Ref.Source, doc.Ref.ID
}
