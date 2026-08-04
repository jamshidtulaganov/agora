package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/logger"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Apply-audit bridge: turn one accepted design_audit finding into an
// implementation issue that adopts a token or extracts a shared component.
// This closes the design-system loop — the audit finds where to build the
// system, and applying a finding creates the scoped codemod task an agent
// executes (opening a PR). The agent is assigned on create, so the normal
// assignment trigger starts the work; no separate slice-action fire needed.

var designAuditBlockRe = regexp.MustCompile("(?s)```design-audit\\s*\\n(.*?)```")

// designAudit is the server-side view of the audit block — only the fields the
// apply endpoint composes a codemod from. Mirrors packages/core/design/audit.ts.
type designAudit struct {
	Duplicates []struct {
		Pattern            string   `json:"pattern"`
		Occurrences        int      `json:"occurrences"`
		SuggestedComponent string   `json:"suggested_component"`
		SampleRefs         []string `json:"sample_refs"`
	} `json:"duplicates"`
	ProposedTokens []struct {
		Name     string   `json:"name"`
		Value    string   `json:"value"`
		Replaces []string `json:"replaces"`
	} `json:"proposed_tokens"`
}

// parseLatestDesignAudit returns the newest agent comment's audit block on the
// issue. ok=false when the issue has none.
func (h *Handler) parseLatestDesignAudit(r *http.Request, issue db.Issue) (designAudit, bool) {
	comments, err := h.Queries.ListCommentsForIssue(r.Context(), db.ListCommentsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       2000,
	})
	if err != nil {
		return designAudit{}, false
	}
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.AuthorType != "agent" {
			continue
		}
		m := designAuditBlockRe.FindStringSubmatch(c.Content)
		if m == nil {
			continue
		}
		var a designAudit
		if json.Unmarshal([]byte(strings.TrimSpace(m[1])), &a) == nil {
			return a, true
		}
	}
	return designAudit{}, false
}

type applyDesignAuditRequest struct {
	Kind  string `json:"kind"`  // "token" | "component"
	Index int    `json:"index"` // index into proposed_tokens / duplicates
}

// ApplyDesignAudit handles POST /api/issues/{id}/design-apply. It reads the
// issue's latest audit block, composes a codemod issue from finding [index],
// and creates it assigned to the design agent (fallback: the caller's own
// agent) so the assignment trigger starts the work.
func (h *Handler) ApplyDesignAudit(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req applyDesignAuditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind != "token" && req.Kind != "component" {
		writeError(w, http.StatusBadRequest, "kind must be 'token' or 'component'")
		return
	}
	if req.Index < 0 {
		writeError(w, http.StatusBadRequest, "index out of range")
		return
	}

	audit, ok := h.parseLatestDesignAudit(r, issue)
	if !ok {
		writeError(w, http.StatusNotFound, "no_design_audit: this issue has no design audit to apply")
		return
	}

	title, description := composeCodemodIssue(audit, req)
	if title == "" {
		writeError(w, http.StatusBadRequest, "index out of range for this finding kind")
		return
	}

	// Assign the design agent (has the design context) — fall back to the
	// caller's own agent. Gate a private designer the caller can't access.
	assigneeType := pgtype.Text{}
	assigneeID := pgtype.UUID{}
	if designer, dok := h.resolveDesignerAgent(r.Context(), issue); dok &&
		h.canAccessPrivateAgent(r.Context(), designer, "member", userID, uuidToString(issue.WorkspaceID)) {
		assigneeType = pgtype.Text{String: "agent", Valid: true}
		assigneeID = designer.ID
	} else if own, ook := h.resolveOwnAgent(r.Context(), issue.WorkspaceID, userID); ook {
		assigneeType = pgtype.Text{String: "agent", Valid: true}
		assigneeID = own.ID
	}

	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID:    issue.WorkspaceID,
		Title:          title,
		Description:    pgtype.Text{String: description, Valid: true},
		Status:         "todo",
		Priority:       "none",
		AssigneeType:   assigneeType,
		AssigneeID:     assigneeID,
		CreatorType:    "member",
		CreatorID:      parseUUID(userID),
		ProjectID:      issue.ProjectID,
		AllowDuplicate: true,
	}, service.IssueCreateOpts{
		ActorID: userID,
		BroadcastPayload: func(iss db.Issue, _ []db.Attachment) map[string]any {
			return map[string]any{"issue": issueToResponse(iss, h.getIssuePrefix(r.Context(), iss.WorkspaceID))}
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create implementation issue: "+err.Error())
		return
	}
	slog.Info("design audit applied", append(logger.RequestAttrs(r),
		"audit_issue_id", uuidToString(issue.ID), "kind", req.Kind, "index", req.Index,
		"impl_issue_id", uuidToString(res.Issue.ID))...)
	writeJSON(w, http.StatusCreated, map[string]any{
		"issue_id": uuidToString(res.Issue.ID),
		"title":    title,
	})
}

// composeCodemodIssue turns one audit finding into an implementation issue's
// title + description (a precise, appearance-preserving refactor spec). Returns
// ("","") when the index is out of range.
func composeCodemodIssue(audit designAudit, req applyDesignAuditRequest) (title, description string) {
	switch req.Kind {
	case "token":
		if req.Index >= len(audit.ProposedTokens) {
			return "", ""
		}
		tk := audit.ProposedTokens[req.Index]
		title = "Adopt design token: " + tk.Name
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Adopt the design token `%s` = `%s` across the codebase.\n\n", tk.Name, tk.Value))
		if len(tk.Replaces) > 0 {
			b.WriteString("Replace these raw values with the token:\n")
			for _, v := range tk.Replaces {
				b.WriteString("- `" + v + "`\n")
			}
			b.WriteString("\n")
		}
		b.WriteString("Add the token to the project's token source (tokens.css / theme or tailwind config) if it does not exist, then replace the raw values with a reference to it. Follow the PROJECT DESIGN SYSTEM manifest.\n\n")
		b.WriteString("This is a PURE token-adoption refactor: the rendered appearance must NOT change. Open a pull request for review.")
		return title, b.String()
	case "component":
		if req.Index >= len(audit.Duplicates) {
			return "", ""
		}
		d := audit.Duplicates[req.Index]
		name := d.SuggestedComponent
		if name == "" {
			name = "shared component"
		}
		title = "Extract shared component: " + name
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Extract a shared `%s` component from the duplicated \"%s\" markup (%d occurrences).\n\n", name, d.Pattern, d.Occurrences))
		if len(d.SampleRefs) > 0 {
			b.WriteString("Occurrences to consolidate:\n")
			for _, ref := range d.SampleRefs {
				b.WriteString("- `" + ref + "`\n")
			}
			b.WriteString("\n")
		}
		b.WriteString("Create the shared component matching the existing design system, replace each occurrence with it, and keep the rendered appearance identical. Follow the PROJECT DESIGN SYSTEM manifest. Open a pull request for review.")
		return title, b.String()
	}
	return "", ""
}
