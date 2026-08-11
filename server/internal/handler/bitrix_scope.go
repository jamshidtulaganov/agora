package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// BITRIX_SYNC_USER_EMAILS is a fail-closed import boundary. When configured, a
// task must belong to one of these responsible-user emails before routing,
// issue creation, comment import, or attachment import can run.
func bitrixSyncUserEmails() map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(os.Getenv("BITRIX_SYNC_USER_EMAILS"), ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out[part] = true
		}
	}
	return out
}

func bitrixUserAllowedEmail(u *bitrix.User) bool {
	allowed := bitrixSyncUserEmails()
	if len(allowed) == 0 {
		return true
	}
	return u != nil && allowed[strings.ToLower(strings.TrimSpace(u.Email))]
}

func bitrixSyncUserStageRules() (map[string]map[string]bool, error) {
	raw := strings.TrimSpace(os.Getenv("BITRIX_SYNC_USER_STAGE_RULES"))
	if raw == "" {
		return nil, nil
	}
	var parsed map[string][]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("parse BITRIX_SYNC_USER_STAGE_RULES: %w", err)
	}
	out := make(map[string]map[string]bool, len(parsed))
	for email, stages := range parsed {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			continue
		}
		set := map[string]bool{}
		for _, stage := range stages {
			if stage = strings.ToLower(strings.TrimSpace(stage)); stage != "" {
				set[stage] = true
			}
		}
		out[email] = set
	}
	return out, nil
}

// bitrixTaskInConfiguredScope enforces the personal-user boundary before the
// task reaches workspace/project routing. Lookup failures fail closed when an
// allowlist exists: a transient user API failure must not broaden a private
// sync into another person's work.
func (h *Handler) bitrixTaskInConfiguredScope(ctx context.Context, task *bitrix.Task, st *bitrixSyncState) bool {
	allowedEmails := bitrixSyncUserEmails()
	if len(allowedEmails) == 0 {
		return true
	}
	responsible := h.bitrixResponsible(ctx, st, task.ResponsibleID)
	if responsible == nil {
		return false
	}
	email := strings.ToLower(strings.TrimSpace(responsible.Email))
	if !allowedEmails[email] {
		return false
	}
	rules, err := bitrixSyncUserStageRules()
	if err != nil {
		slog.Warn("bitrix sync: invalid user stage rules; failing closed", "error", err)
		return false
	}
	allowedStages, restricted := rules[email]
	if !restricted {
		return true
	}
	stageName := h.bitrixStageName(ctx, st, task.GroupID, task.StageID)
	return allowedStages[strings.ToLower(strings.TrimSpace(stageName))]
}

// bitrixTaskHasStageRestriction reports whether this task's responsible user
// is governed by BITRIX_SYNC_USER_STAGE_RULES. It is used to unarchive a review
// issue when the task re-enters its allowed stage without reviving ordinary
// Bitrix issues a human intentionally archived.
func (h *Handler) bitrixTaskHasStageRestriction(ctx context.Context, task *bitrix.Task, st *bitrixSyncState) bool {
	responsible := h.bitrixResponsible(ctx, st, task.ResponsibleID)
	if responsible == nil {
		return false
	}
	rules, err := bitrixSyncUserStageRules()
	if err != nil {
		return false
	}
	_, restricted := rules[strings.ToLower(strings.TrimSpace(responsible.Email))]
	return restricted
}

// bitrixReviewSquadAssignee routes stage-restricted personal review work to a
// dedicated squad. Ordinary personal tasks keep their Bitrix-responsible human
// assignee. The squad id is deployment configuration so no workspace identity
// is hard-coded in application logic.
func (h *Handler) bitrixReviewSquadAssignee(ctx context.Context, task *bitrix.Task, st *bitrixSyncState) (pgtype.Text, pgtype.UUID, bool) {
	if !h.bitrixTaskHasStageRestriction(ctx, task, st) {
		return pgtype.Text{}, pgtype.UUID{}, false
	}
	raw := strings.TrimSpace(os.Getenv("BITRIX_REVIEW_SQUAD_ID"))
	if raw == "" {
		return pgtype.Text{}, pgtype.UUID{}, false
	}
	id, err := util.ParseUUID(raw)
	if err != nil {
		slog.Warn("bitrix sync: invalid BITRIX_REVIEW_SQUAD_ID", "error", err)
		return pgtype.Text{}, pgtype.UUID{}, false
	}
	return pgtype.Text{String: "squad", Valid: true}, id, true
}

// bitrixConfiguredUserIDs resolves the configured emails to portal user ids.
// It drives discovery polling even before the Agora account has an explicit
// external-identity link. An empty email allowlist intentionally returns nil:
// the caller then uses the legacy linked-user discovery path.
func (h *Handler) bitrixConfiguredUserIDs(ctx context.Context, st *bitrixSyncState) ([]string, error) {
	allowed := bitrixSyncUserEmails()
	if len(allowed) == 0 {
		return nil, nil
	}
	users, err := st.client.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users for personal sync: %w", err)
	}
	ids := make([]string, 0, len(allowed))
	for i := range users {
		u := users[i]
		if !allowed[strings.ToLower(strings.TrimSpace(u.Email))] {
			continue
		}
		id := strings.TrimSpace(u.ID)
		if id == "" {
			continue
		}
		cached := u
		st.userCache[id] = &cached
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("none of BITRIX_SYNC_USER_EMAILS exists in the Bitrix directory")
	}
	return ids, nil
}
