package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/auth"
	"github.com/jamshidtulaganov/agora/server/internal/logger"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

const accountOwnsWorkspacesCode = "account_owns_workspaces"

type deleteAccountRequest struct {
	Confirmation string `json:"confirmation"`
}

type accountWorkspaceSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type accountMembership struct {
	MemberID    pgtype.UUID
	WorkspaceID pgtype.UUID
	Role        string
	Name        string
	Slug        string
}

type accountRevocation struct {
	Membership accountMembership
	Result     revocationResult
}

// DeleteMe permanently deletes the authenticated human's account. The user
// must type their exact email, and deletion is refused while they are the last
// owner of any workspace. That explicit ownership handoff keeps shared data
// from being silently deleted or left without an administrator.
//
// Every membership/runtime revocation and the user delete share one
// transaction. Either the whole account is removed or no workspace is
// modified. The FK policy in migration 183 preserves shared historical rows
// while cascades revoke personal credentials and identities.
func (h *Handler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req deleteAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userUUID := parseUUID(userID)
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}
	defer tx.Rollback(r.Context())

	var email string
	if err := tx.QueryRow(r.Context(), `SELECT email FROM "user" WHERE id = $1 FOR UPDATE`, userUUID).Scan(&email); err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if req.Confirmation != email {
		writeError(w, http.StatusBadRequest, "confirmation must exactly match your email address")
		return
	}

	// The PAT rows cascade with the user, but Redis may hold a successful
	// lookup for up to ten minutes. Capture every hash inside the transaction
	// so those cache entries can be invalidated immediately after commit.
	patRows, err := tx.Query(r.Context(), `
		SELECT token_hash
		FROM personal_access_token
		WHERE user_id = $1
		FOR UPDATE
	`, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}
	var patHashes []string
	for patRows.Next() {
		var hash string
		if err := patRows.Scan(&hash); err != nil {
			patRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to delete account")
			return
		}
		patHashes = append(patHashes, hash)
	}
	if err := patRows.Err(); err != nil {
		patRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}
	patRows.Close()

	// Lock both the caller's memberships and their workspaces. A concurrent
	// member insert needs a FK key-share lock on workspace and therefore waits
	// until this ownership decision commits or rolls back.
	rows, err := tx.Query(r.Context(), `
		SELECT m.id, m.workspace_id, m.role, w.name, w.slug
		FROM member m
		JOIN workspace w ON w.id = m.workspace_id
		WHERE m.user_id = $1
		ORDER BY w.created_at, w.id
		FOR UPDATE OF m, w
	`, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	var memberships []accountMembership
	for rows.Next() {
		var membership accountMembership
		if err := rows.Scan(
			&membership.MemberID,
			&membership.WorkspaceID,
			&membership.Role,
			&membership.Name,
			&membership.Slug,
		); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to delete account")
			return
		}
		memberships = append(memberships, membership)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}
	rows.Close()

	var blocked []accountWorkspaceSummary
	for _, membership := range memberships {
		if membership.Role != "owner" {
			continue
		}

		ownerRows, err := tx.Query(r.Context(), `
			SELECT role
			FROM member
			WHERE workspace_id = $1
			FOR UPDATE
		`, membership.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete account")
			return
		}
		ownerCount := 0
		for ownerRows.Next() {
			var role string
			if err := ownerRows.Scan(&role); err != nil {
				ownerRows.Close()
				writeError(w, http.StatusInternalServerError, "failed to delete account")
				return
			}
			if role == "owner" {
				ownerCount++
			}
		}
		if err := ownerRows.Err(); err != nil {
			ownerRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to delete account")
			return
		}
		ownerRows.Close()

		if ownerCount <= 1 {
			blocked = append(blocked, accountWorkspaceSummary{
				ID:   uuidToString(membership.WorkspaceID),
				Name: membership.Name,
				Slug: membership.Slug,
			})
		}
	}

	if len(blocked) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":       accountOwnsWorkspacesCode,
			"error":      "transfer ownership or delete these workspaces first",
			"workspaces": blocked,
		})
		return
	}

	qtx := h.Queries.WithTx(tx)
	revocations := make([]accountRevocation, 0, len(memberships))
	for _, membership := range memberships {
		result, err := h.revokeAndRemoveMemberTx(
			r.Context(),
			qtx,
			membership.WorkspaceID,
			userUUID,
			membership.MemberID,
			userUUID,
		)
		if err != nil {
			slog.Warn("account membership revocation failed", append(logger.RequestAttrs(r),
				"error", err,
				"user_id", userID,
				"workspace_id", uuidToString(membership.WorkspaceID),
			)...)
			writeError(w, http.StatusInternalServerError, "failed to delete account")
			return
		}
		revocations = append(revocations, accountRevocation{Membership: membership, Result: result})
	}

	if _, err := tx.Exec(r.Context(), `DELETE FROM "user" WHERE id = $1`, userUUID); err != nil {
		slog.Warn("account delete failed", append(logger.RequestAttrs(r), "error", err, "user_id", userID)...)
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Warn("account delete commit failed", append(logger.RequestAttrs(r), "error", err, "user_id", userID)...)
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	// The response may be disconnected as soon as the client observes 204.
	// Post-commit security invalidations must still finish in that case.
	postCommitCtx := context.WithoutCancel(r.Context())
	for _, hash := range patHashes {
		h.PATCache.Invalidate(postCommitCtx, hash)
	}
	for _, revocation := range revocations {
		workspaceID := uuidToString(revocation.Membership.WorkspaceID)
		h.MembershipCache.Invalidate(postCommitCtx, userID, workspaceID)
		logRevocation(revocation.Result, workspaceID, userID, "account_deleted", true)
		h.publishRevocation(postCommitCtx, revocation.Result, workspaceID, "member", userID)
		h.publish(protocol.EventMemberRemoved, workspaceID, "member", userID, map[string]any{
			"member_id":    uuidToString(revocation.Membership.MemberID),
			"workspace_id": workspaceID,
			"user_id":      userID,
		})
	}

	auth.ClearAuthCookies(w)
	if h.CFSigner != nil {
		for _, cookie := range h.CFSigner.ExpiredCookies() {
			http.SetCookie(w, cookie)
		}
	}
	slog.Info("account deleted", append(logger.RequestAttrs(r), "user_id", userID, "memberships_removed", len(memberships))...)
	w.WriteHeader(http.StatusNoContent)
}
