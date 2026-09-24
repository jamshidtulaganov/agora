package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// POST /api/zoho-projects/welcome-emails
//
// The second half of the Zoho workspace migration: the people the migration
// made members get one "your team is on Agora now" email each, listing the
// migrated workspaces they are in, and — if they have never signed in — are
// sent into the first-login member setup (onboarded_at cleared) so their
// first visit walks them through photo, notifications and a tour.
//
//	{"dry_run": true}                          plan everyone, send nothing
//	{"emails": ["jamshidjon.t1@tsst.ai"]}      send to just these (a test)
//	{"resend": true}                           include people already welcomed
//
// Recipients are limited to members of migrated workspaces the CALLER owns,
// so the endpoint can never mail a workspace the caller does not run. Gated
// by AGORA_ZOHO_MIGRATE like the migration itself.

type ZohoWelcomeRequest struct {
	DryRun bool     `json:"dry_run"`
	Emails []string `json:"emails"`
	Resend bool     `json:"resend"`
}

type ZohoWelcomeRecipient struct {
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Workspaces  []string `json:"workspaces"`
	AlreadySent bool     `json:"already_sent"`
	Sent        bool     `json:"sent"`
	SetupReset  bool     `json:"setup_reset"`
	Skipped     string   `json:"skipped,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type ZohoWelcomeResponse struct {
	DryRun     bool                   `json:"dry_run"`
	Recipients []ZohoWelcomeRecipient `json:"recipients"`
	Totals     struct {
		Recipients int `json:"recipients"`
		ToSend     int `json:"to_send"`
		Sent       int `json:"sent"`
		Failed     int `json:"failed"`
	} `json:"totals"`
}

type welcomeTarget struct {
	id          pgtype.UUID
	email       string
	name        string
	alreadySent bool
	workspaces  []string
}

// SendZohoWelcomeEmails handles POST /api/zoho-projects/welcome-emails.
func (h *Handler) SendZohoWelcomeEmails(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !zohoMigrateEnabled() {
		writeError(w, http.StatusForbidden, "zoho workspace migration is disabled (set AGORA_ZOHO_MIGRATE=1)")
		return
	}
	var req ZohoWelcomeRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	only := map[string]bool{}
	for _, e := range req.Emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			only[e] = true
		}
	}

	targets, err := h.zohoWelcomeTargets(r, parseUUID(userID))
	if err != nil {
		slog.Warn("zoho welcome: list recipients failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list recipients")
		return
	}

	resp := ZohoWelcomeResponse{DryRun: req.DryRun, Recipients: []ZohoWelcomeRecipient{}}
	for _, t := range targets {
		if len(only) > 0 && !only[t.email] {
			continue
		}
		rec := ZohoWelcomeRecipient{
			Email: t.email, Name: t.name, Workspaces: t.workspaces, AlreadySent: t.alreadySent,
		}
		switch {
		case isTelegramSyntheticEmail(t.email) || isUndeliverableEmailDomain(t.email):
			rec.Skipped = "no deliverable email address"
		case t.alreadySent && !req.Resend:
			rec.Skipped = "already welcomed"
		}
		resp.Totals.Recipients++
		if rec.Skipped == "" {
			resp.Totals.ToSend++
			if !req.DryRun {
				h.sendZohoWelcome(r, t, &rec)
				if rec.Sent {
					resp.Totals.Sent++
				} else {
					resp.Totals.Failed++
				}
			}
		}
		resp.Recipients = append(resp.Recipients, rec)
	}
	writeJSON(w, http.StatusOK, resp)
}

// zohoWelcomeTargets lists everyone (except the caller) who is a member of a
// migrated workspace the caller owns, with the migrated workspaces they are in.
func (h *Handler) zohoWelcomeTargets(r *http.Request, callerID pgtype.UUID) ([]welcomeTarget, error) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT u.id, lower(u.email), u.name, u.welcome_sent_at IS NOT NULL, w.name
		  FROM workspace w
		  JOIN member owner ON owner.workspace_id = w.id AND owner.user_id = $1 AND owner.role = 'owner'
		  JOIN member m ON m.workspace_id = w.id
		  JOIN "user" u ON u.id = m.user_id
		 WHERE w.settings ? $2 AND u.id <> $1
		 ORDER BY lower(u.email), w.name`,
		callerID, zohoWorkspaceProjectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]*welcomeTarget{}
	var order []string
	for rows.Next() {
		var t welcomeTarget
		var wsName string
		if err := rows.Scan(&t.id, &t.email, &t.name, &t.alreadySent, &wsName); err != nil {
			return nil, err
		}
		key := util.UUIDToString(t.id)
		existing, seen := byID[key]
		if !seen {
			t.workspaces = []string{}
			byID[key] = &t
			order = append(order, key)
			existing = &t
		}
		existing.workspaces = append(existing.workspaces, wsName)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]welcomeTarget, 0, len(order))
	for _, key := range order {
		t := byID[key]
		sort.Strings(t.workspaces)
		out = append(out, *t)
	}
	return out, nil
}

// sendZohoWelcome emails one person, then records it and — only if they have
// never signed in — sends them into the member setup. The send comes first so
// a failed email leaves the account exactly as it was.
func (h *Handler) sendZohoWelcome(r *http.Request, t welcomeTarget, rec *ZohoWelcomeRecipient) {
	if err := h.EmailService.SendWelcomeEmail(t.email, t.name, t.workspaces); err != nil {
		slog.Warn("zoho welcome: send failed", "email", t.email, "error", err)
		rec.Error = "send failed"
		return
	}
	rec.Sent = true
	if err := h.Queries.MarkUserWelcomeSent(r.Context(), t.id); err != nil {
		slog.Warn("zoho welcome: mark sent failed", "email", t.email, "error", err)
	}
	reset, err := h.Queries.ResetOnboardingIfNeverSignedIn(r.Context(), t.id)
	if err != nil {
		slog.Warn("zoho welcome: reset onboarding failed", "email", t.email, "error", err)
		return
	}
	rec.SetupReset = reset
	slog.Info("zoho welcome: sent", "email", t.email, "workspaces", len(t.workspaces), "setup_reset", reset)
}
