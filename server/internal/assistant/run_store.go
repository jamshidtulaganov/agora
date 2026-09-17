package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

const runLeaseDuration = 30 * time.Second

var ErrRequestConflict = errors.New("assistant: request id was used with different content or context")

type runTxStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}
type runDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type RunContext struct {
	WorkspaceID   *string  `json:"workspace_id"`
	Timezone      string   `json:"timezone,omitempty"`
	ProjectID     *string  `json:"project_id,omitempty"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
}

type RunRecord struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	MessageID  string     `json:"message_id"`
	Status     string     `json:"status"`
	ActiveTool *string    `json:"active_tool"`
	Error      *string    `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Version    int64      `json:"version"`
	Context    RunContext `json:"context"`
}

type AcceptedRun struct {
	Run       RunRecord
	Message   db.AssistantMessage
	Duplicate bool
}

func (s *Service) FindRequest(ctx context.Context, sessionID, requestID, content, requestContext string) (*AcceptedRun, error) {
	var runID, messageID, oldContent, oldContext string
	var createdAt time.Time
	err := s.Store.QueryRow(ctx, `SELECT r.id::text,r.message_id::text,m.created_at,r.request_content,r.request_context FROM assistant_run r JOIN assistant_message m ON m.id=r.message_id WHERE r.session_id=$1 AND r.request_id=$2`, sessionID, requestID).Scan(&runID, &messageID, &createdAt, &oldContent, &oldContext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if oldContent != content || oldContext != requestContext {
		return nil, ErrRequestConflict
	}
	r, err := scanRun(s.Store.QueryRow(ctx, `SELECT `+runColumns+` FROM assistant_run WHERE id=$1`, runID))
	if err != nil {
		return nil, err
	}
	return &AcceptedRun{Run: r, Message: db.AssistantMessage{ID: util.MustParseUUID(messageID), CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true}}, Duplicate: true}, nil
}

func (s *Service) recoverExpired(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	rows, err := s.Store.Query(ctx, `WITH expired AS (UPDATE assistant_run SET status='interrupted', error='the server stopped before this run finished; any pending write needs review before retry', active_tool=NULL, finished_at=now(), updated_at=now(), version=version+1, lease_owner=NULL, lease_expires_at=NULL WHERE status IN ('queued','running') AND (lease_expires_at IS NULL OR lease_expires_at < now()) RETURNING id,session_id,user_id), uncertain AS (UPDATE assistant_operation SET status='uncertain',updated_at=now() WHERE run_id IN (SELECT id FROM expired) AND status='pending' RETURNING id) SELECT id::text,session_id::text,user_id::text FROM expired`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, sessionID, userID string
		if err := rows.Scan(&runID, &sessionID, &userID); err != nil {
			return err
		}
		s.publish(protocol.EventAssistantRunFinished, protocol.AssistantRunFinishedPayload{UserID: userID, SessionID: sessionID, RunID: runID, Status: "interrupted", Error: "the server stopped before this run finished; review pending writes before retrying"})
	}
	return rows.Err()
}

func (s *Service) StartRecovery(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			check, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := s.recoverExpired(check); err != nil && ctx.Err() == nil {
				slog.Warn("assistant: run recovery failed", "error", err)
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func scanRun(row pgx.Row) (RunRecord, error) {
	var r RunRecord
	var workspace, tool, runErr *string
	err := row.Scan(&r.ID, &r.SessionID, &r.MessageID, &r.Status, &tool, &runErr, &r.CreatedAt, &r.UpdatedAt, &r.FinishedAt, &r.Version, &workspace, &r.Context.Timezone, &r.Context.ProjectID, &r.Context.AttachmentIDs)
	r.Context.WorkspaceID, r.ActiveTool, r.Error = workspace, tool, runErr
	return r, err
}

const runColumns = `id::text, session_id::text, message_id::text, status, active_tool, error, created_at, updated_at, finished_at, version, context_workspace_id::text, context_timezone, context_project_id::text, context_attachment_ids::text[]`

func (s *Service) GetRun(ctx context.Context, runID, userID string) (RunRecord, error) {
	if err := s.recoverExpired(ctx); err != nil {
		return RunRecord{}, err
	}
	return scanRun(s.Store.QueryRow(ctx, `SELECT `+runColumns+` FROM assistant_run WHERE id=$1 AND user_id=$2`, runID, userID))
}

func (s *Service) LatestRun(ctx context.Context, sessionID, userID string) (*RunRecord, error) {
	if err := s.recoverExpired(ctx); err != nil {
		return nil, err
	}
	r, err := scanRun(s.Store.QueryRow(ctx, `SELECT `+runColumns+` FROM assistant_run WHERE session_id=$1 AND user_id=$2 ORDER BY created_at DESC,id DESC LIMIT 1`, sessionID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) ListRuns(ctx context.Context, sessionID, userID string) ([]RunRecord, error) {
	if err := s.recoverExpired(ctx); err != nil {
		return nil, err
	}
	rows, err := s.Store.Query(ctx, `SELECT `+runColumns+` FROM assistant_run WHERE session_id=$1 AND user_id=$2 ORDER BY created_at DESC, id DESC LIMIT 50`, sessionID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRecord
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out == nil {
		out = []RunRecord{}
	}
	return out, nil
}

// AcceptRun locks the session row so request replay, active-run exclusion, and
// message/run insertion have one commit point across server processes.
func (s *Service) AcceptRun(ctx context.Context, session db.AssistantSession, userID, content string, requestID *string, requestContext string, context RunContext, snapshot []byte) (AcceptedRun, error) {
	if s.TxStarter == nil || s.Store == nil {
		return AcceptedRun{}, errors.New("assistant run store unavailable")
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return AcceptedRun{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM assistant_session WHERE id=$1 AND user_id=$2 FOR UPDATE`, session.ID, userID); err != nil {
		return AcceptedRun{}, err
	}
	if requestID != nil {
		var oldContent, oldContext string
		var messageID string
		var createdAt time.Time
		var oldRunID string
		err = tx.QueryRow(ctx, `SELECT r.id::text, r.message_id::text, m.created_at, r.request_content, r.request_context FROM assistant_run r JOIN assistant_message m ON m.id=r.message_id WHERE r.session_id=$1 AND r.request_id=$2`, session.ID, *requestID).Scan(&oldRunID, &messageID, &createdAt, &oldContent, &oldContext)
		if err == nil {
			if oldContent != content || oldContext != requestContext {
				return AcceptedRun{}, ErrRequestConflict
			}
			run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM assistant_run WHERE id=$1`, oldRunID))
			if err != nil {
				return AcceptedRun{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return AcceptedRun{}, err
			}
			return AcceptedRun{Run: run, Message: db.AssistantMessage{ID: util.MustParseUUID(messageID), CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true}}, Duplicate: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return AcceptedRun{}, err
		}
	}
	// Expired work is interrupted, never replayed: effects may already have landed.
	if _, err := tx.Exec(ctx, `WITH expired AS (UPDATE assistant_run SET status='interrupted', error='the server stopped before this run finished; any pending write needs review before retry', active_tool=NULL, finished_at=now(), updated_at=now(), version=version+1, lease_owner=NULL, lease_expires_at=NULL WHERE session_id=$1 AND status IN ('queued','running') AND (lease_expires_at IS NULL OR lease_expires_at < now()) RETURNING id) UPDATE assistant_operation SET status='uncertain',updated_at=now() WHERE run_id IN (SELECT id FROM expired) AND status='pending'`, session.ID); err != nil {
		return AcceptedRun{}, err
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assistant_run WHERE session_id=$1 AND status IN ('queued','running'))`, session.ID).Scan(&busy); err != nil {
		return AcceptedRun{}, err
	}
	if busy {
		return AcceptedRun{}, ErrRunInProgress
	}
	message, err := db.New(tx).CreateAssistantMessage(ctx, db.CreateAssistantMessageParams{SessionID: session.ID, Role: "user", Content: content})
	if err != nil {
		return AcceptedRun{}, err
	}
	owner := uuid.NewString()
	var workspace any
	if context.WorkspaceID != nil {
		workspace = *context.WorkspaceID
	}
	var rid any
	if requestID != nil {
		rid = *requestID
	}
	var project any
	if context.ProjectID != nil {
		project = *context.ProjectID
	}
	if len(snapshot) == 0 {
		snapshot = []byte(`{}`)
	}
	// A nil attachment slice must land as the empty array, not SQL NULL: the
	// column is NOT NULL and its DEFAULT applies only when the column is
	// omitted, never to an explicit NULL. Most sends carry no files.
	attachments := context.AttachmentIDs
	if attachments == nil {
		attachments = []string{}
	}
	run, err := scanRun(tx.QueryRow(ctx, `INSERT INTO assistant_run (session_id,user_id,message_id,request_id,request_content,request_context,status,context_workspace_id,context_timezone,context_project_id,context_attachment_ids,context_snapshot,lease_owner,lease_expires_at) VALUES ($1,$2,$3,$4,$5,$6,'running',$7,$8,$9,$10,$11,$12,now()+interval '30 seconds') RETURNING `+runColumns, session.ID, userID, message.ID, rid, content, requestContext, workspace, context.Timezone, project, attachments, snapshot, owner))
	if err != nil {
		return AcceptedRun{}, fmt.Errorf("insert assistant run: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE assistant_message SET run_id=$1 WHERE id=$2`, run.ID, message.ID); err != nil {
		return AcceptedRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AcceptedRun{}, err
	}
	s.launchStoredRun(run.ID, util.UUIDToString(session.ID), userID, owner)
	return AcceptedRun{Run: run, Message: message}, nil
}

func (s *Service) updateRun(ctx context.Context, runID, owner, status, errorMessage string) (bool, error) {
	var errVal any
	if errorMessage != "" {
		errVal = errorMessage
	}
	if _, err := s.Store.Exec(ctx, `UPDATE assistant_operation SET status='uncertain',updated_at=now() WHERE run_id=$1 AND status='pending'`, runID); err != nil {
		return false, err
	}
	tag, err := s.Store.Exec(ctx, `UPDATE assistant_run SET status=$3,error=$4,active_tool=NULL,finished_at=now(),updated_at=now(),version=version+1,lease_owner=NULL,lease_expires_at=NULL WHERE id=$1 AND lease_owner=$2 AND status='running'`, runID, owner, status, errVal)
	return tag.RowsAffected() == 1, err
}

func (s *Service) launchStoredRun(runID, sessionID, userID, owner string) {
	ctx, cancel := context.WithTimeout(context.Background(), MaxRunDuration)
	s.mu.Lock()
	s.runs[runID] = &runHandle{sessionID: sessionID, userID: userID, owner: owner, cancel: cancel}
	s.active[sessionID] = runID
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer s.finishRun(runID)
		defer func() {
			if rec := recover(); rec != nil {
				final, c := context.WithTimeout(context.Background(), 5*time.Second)
				defer c()
				updated, _ := s.updateRun(final, runID, owner, "failed", "the assistant stopped unexpectedly")
				if updated {
					s.publish(protocol.EventAssistantRunFinished, protocol.AssistantRunFinishedPayload{UserID: userID, SessionID: sessionID, RunID: runID, Status: "failed", Error: "the assistant stopped unexpectedly"})
				}
			}
		}()
		go s.heartbeatRun(ctx, runID, owner, cancel)
		s.Run(ctx, sessionID, runID, userID)
	}()
}

func (s *Service) heartbeatRun(ctx context.Context, runID, owner string, cancel context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, c := context.WithTimeout(context.Background(), 5*time.Second)
			var status string
			err := s.Store.QueryRow(check, `UPDATE assistant_run SET lease_expires_at=now()+interval '30 seconds', updated_at=now() WHERE id=$1 AND lease_owner=$2 AND status='running' AND cancel_requested=false AND lease_expires_at>now() RETURNING status`, runID, owner).Scan(&status)
			c()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

// RequestCancel persists intent while retaining the active slot. The worker
// acknowledges terminal cancellation after it stops executing.
func (s *Service) RequestCancel(ctx context.Context, runID, userID string) (bool, error) {
	if s.Store == nil {
		return s.CancelRun(runID, userID), nil
	}
	var id string
	err := s.Store.QueryRow(ctx, `UPDATE assistant_run SET cancel_requested=true,updated_at=now(),version=version+1 WHERE id=$1 AND user_id=$2 AND status IN ('queued','running') RETURNING id::text`, runID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.CancelRun(runID, userID)
	return true, nil
}
