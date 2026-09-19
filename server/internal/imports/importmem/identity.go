// The identity and job halves of the fake.
package importmem

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// ErrIdentityClaimed mirrors handler.errExternalIdentityClaimed: a
// (provider, external_id) already bound to a DIFFERENT user is not overwritten.
// The link-steal guard is exactly the property an importer needs when two
// sources claim one person.
var ErrIdentityClaimed = errors.New("external identity already linked to another user")

func identityKey(provider, externalID string) string { return provider + "/" + externalID }

func (s *Store) UserIDByExternalIdentity(_ context.Context, provider, externalID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Identities[identityKey(provider, externalID)], nil
}

func (s *Store) LinkExternalIdentity(_ context.Context, provider, externalID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("LinkExternalIdentity")
	k := identityKey(provider, externalID)
	if owner, ok := s.Identities[k]; ok && owner != userID {
		return ErrIdentityClaimed
	}
	s.Identities[k] = userID
	return nil
}

// MemberUserIDByEmail matches case-folded, and ONLY against members of this
// workspace. Matching against all users would let an import bind a stranger's
// account, which is why the query is workspace-scoped by contract.
func (s *Store) MemberUserIDByEmail(_ context.Context, workspaceID, email string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", nil
	}
	roster := s.Members[workspaceID]
	for _, u := range s.Users {
		if strings.ToLower(u.Email) != email {
			continue
		}
		id := util.UUIDToString(u.ID)
		if _, ok := roster[id]; ok {
			return id, nil
		}
	}
	return "", nil
}

func (s *Store) EnsureUser(_ context.Context, email, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("EnsureUser")
	for _, u := range s.Users {
		if strings.EqualFold(u.Email, email) {
			return util.UUIDToString(u.ID), nil
		}
	}
	u := db.User{ID: s.nextID(), Name: name, Email: email}
	s.Users = append(s.Users, u)
	return util.UUIDToString(u.ID), nil
}

func (s *Store) EnsureMember(_ context.Context, workspaceID, userID, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("EnsureMember")
	if s.Members[workspaceID] == nil {
		s.Members[workspaceID] = map[string]string{}
	}
	if _, ok := s.Members[workspaceID][userID]; !ok {
		s.Members[workspaceID][userID] = role
	}
	return nil
}

// AddMember seeds an existing workspace member, for the "email matches a
// member" resolution step.
func (s *Store) AddMember(workspaceID pgtype.UUID, name, email, role string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := db.User{ID: s.nextID(), Name: name, Email: email}
	s.Users = append(s.Users, u)
	ws := util.UUIDToString(workspaceID)
	if s.Members[ws] == nil {
		s.Members[ws] = map[string]string{}
	}
	id := util.UUIDToString(u.ID)
	s.Members[ws][id] = role
	return id
}

// UserByID is a test convenience.
func (s *Store) UserByID(id string) (db.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		if util.UUIDToString(u.ID) == id {
			return u, true
		}
	}
	return db.User{}, false
}

// --- import_job -------------------------------------------------------------

func (s *Store) CreateImportJob(_ context.Context, arg db.CreateImportJobParams) (db.ImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("CreateImportJob")
	job := db.ImportJob{
		ID:           s.nextID(),
		WorkspaceID:  arg.WorkspaceID,
		ConnectionID: arg.ConnectionID,
		Source:       arg.Source,
		Status:       arg.Status,
		Scope:        arg.Scope,
		Totals:       []byte(`{}`),
		Failures:     []byte(`[]`),
		CreatedBy:    arg.CreatedBy,
	}
	s.Jobs = append(s.Jobs, job)
	return job, nil
}

func (s *Store) GetImportJob(_ context.Context, arg db.GetImportJobParams) (db.ImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.Jobs {
		if key(j.ID) == key(arg.ID) && key(j.WorkspaceID) == key(arg.WorkspaceID) {
			return j, nil
		}
	}
	return db.ImportJob{}, pgx.ErrNoRows
}

// GetActiveImportJob is the refusal that replaced the Bitrix process-global: a
// second import in this workspace points at the live one rather than cancelling
// it. Newest first, matching the query's ORDER BY created_at DESC.
func (s *Store) GetActiveImportJob(_ context.Context, workspaceID pgtype.UUID) (db.ImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.Jobs) - 1; i >= 0; i-- {
		j := s.Jobs[i]
		if key(j.WorkspaceID) != key(workspaceID) {
			continue
		}
		switch j.Status {
		case "pending", "dry_run", "awaiting_confirm", "running":
			return j, nil
		}
	}
	return db.ImportJob{}, pgx.ErrNoRows
}

func (s *Store) SetImportJobPlan(_ context.Context, arg db.SetImportJobPlanParams) (db.ImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("SetImportJobPlan")
	for i := range s.Jobs {
		if key(s.Jobs[i].ID) != key(arg.ID) || key(s.Jobs[i].WorkspaceID) != key(arg.WorkspaceID) {
			continue
		}
		s.Jobs[i].Status = arg.Status
		s.Jobs[i].Plan = arg.Plan
		s.Jobs[i].Mapping = arg.Mapping
		return s.Jobs[i], nil
	}
	return db.ImportJob{}, pgx.ErrNoRows
}

// StartImportJob is the guarded claim. The status filter is what makes a retry,
// or two servers racing the same confirm, produce exactly one run: the loser
// gets no row (pgx.ErrNoRows), not a second import.
func (s *Store) StartImportJob(_ context.Context, arg db.StartImportJobParams) (db.ImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("StartImportJob")
	for i := range s.Jobs {
		if key(s.Jobs[i].ID) != key(arg.ID) || key(s.Jobs[i].WorkspaceID) != key(arg.WorkspaceID) {
			continue
		}
		switch s.Jobs[i].Status {
		case "pending", "dry_run", "awaiting_confirm":
			s.Jobs[i].Status = "running"
			s.Jobs[i].StartedAt = pgtype.Timestamptz{Valid: true}
			return s.Jobs[i], nil
		}
		return db.ImportJob{}, pgx.ErrNoRows
	}
	return db.ImportJob{}, pgx.ErrNoRows
}

func (s *Store) UpdateImportJobProgress(_ context.Context, arg db.UpdateImportJobProgressParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("UpdateImportJobProgress")
	for i := range s.Jobs {
		if key(s.Jobs[i].ID) == key(arg.ID) && key(s.Jobs[i].WorkspaceID) == key(arg.WorkspaceID) {
			s.Jobs[i].Totals = arg.Totals
			return nil
		}
	}
	return nil
}

func (s *Store) FinishImportJob(_ context.Context, arg db.FinishImportJobParams) (db.ImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("FinishImportJob")
	for i := range s.Jobs {
		if key(s.Jobs[i].ID) != key(arg.ID) || key(s.Jobs[i].WorkspaceID) != key(arg.WorkspaceID) {
			continue
		}
		s.Jobs[i].Status = arg.Status
		s.Jobs[i].Totals = arg.Totals
		s.Jobs[i].Failures = arg.Failures
		s.Jobs[i].ArtifactID = arg.ArtifactID
		s.Jobs[i].FinishedAt = pgtype.Timestamptz{Valid: true}
		return s.Jobs[i], nil
	}
	return db.ImportJob{}, pgx.ErrNoRows
}
