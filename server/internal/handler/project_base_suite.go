package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Project base suite build — seeds the STANDING golden-path regression suite
// (the project-scoped test cases every run_qa / run_test_cases executes) by
// having the QA squad's leader author cases from the project's QA manifest
// flows. Reuses the whole existing pipeline instead of a parallel one:
//
//   tracking issue → QA lead authors ```test-cases (existing capture attaches
//   them to the issue) → lead verifies on the QA box → issue moves to done →
//   maybePromoteTestCasesOnDone copies them into the project base suite.
//
// POST /api/projects/{id}/base-suite/build. Mirrors BuildProjectKnowledge's
// guards; 400s carry the actionable reason.

const baseSuitePromptTmpl = "[AUTOMATED DIRECTIVE — base-suite authoring] " +
	"Author the project's GOLDEN-PATH BASE SUITE — the standing regression cases every future " +
	"QA run executes. Source of truth: the PROJECT QA MANIFEST in your context (its flows + routes) and the project's " +
	"risk map (critical modules FIRST: money, orders, stock). For each golden path author ONE tight automated case — " +
	"title prefixed with its layer tag ([e2e]/[api]/[smoke]), concrete steps, a deterministic expected assertion, and " +
	"BOTH categories where it matters (positive golden path + the negative guard). " +
	"Emit them in ONE fenced ```test-cases code block containing ONLY a JSON ARRAY (the platform parses JSON — a " +
	"Markdown/YAML list is NOT captured and the whole suite is silently dropped): " +
	"`[{\"title\":\"<short>\",\"steps\":\"<numbered steps, newline-separated>\",\"expected\":\"<expected result>\"," +
	"\"kind\":\"manual\"|\"automated\",\"category\":\"positive\"|\"negative\",\"script\":\"<self-contained Playwright " +
	"ESM module runnable with plain node — REQUIRED for every [e2e]/[api] automated case; import { chromium } from " +
	"\\\"playwright\\\", use the QA manifest base_url+auth+routes, assert by deterministic DOM/HTTP signal, exit 0/1>\"}]` " +
	"— `automated` for a case a script can run deterministically, `manual` for a human click-through; the JSON must be " +
	"valid and self-contained (a short summary may precede it). Then VERIFY each case actually runs against the " +
	"QA box (mark blocked — never invent — anything needing credentials the manifest does not have). When the cases " +
	"are captured and verified: attach the `qa:pass` label (your verification IS this issue's QA — without the label " +
	"the status gate bounces the transition), then set THIS issue's status to done — the platform then promotes the " +
	"cases into the standing project suite automatically. Do NOT touch product code."

// BuildProjectBaseSuite manually triggers the QA-lead authoring run.
func (h *Handler) BuildProjectBaseSuite(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: project.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	if !projectHasQAManifest(project.Settings) {
		writeError(w, http.StatusBadRequest, "set the project's QA manifest first — the base suite is authored from its flows")
		return
	}
	// Idempotency: one open authoring run per project. A repeat call while the
	// previous tracking issue is still open would mint a second issue and — via
	// promotion on done — permanently duplicate the standing suite.
	if len(project.Settings) > 0 {
		var s struct {
			TrackingIssue string `json:"base_suite_issue_id"`
		}
		if json.Unmarshal(project.Settings, &s) == nil && strings.TrimSpace(s.TrackingIssue) != "" {
			if prevID, perr := util.ParseUUID(strings.TrimSpace(s.TrackingIssue)); perr == nil {
				if prev, ierr := h.Queries.GetIssue(r.Context(), prevID); ierr == nil &&
					prev.Status != "done" && prev.Status != "cancelled" {
					writeError(w, http.StatusConflict,
						"a base-suite authoring run is already open (issue "+util.UUIDToString(prev.ID)+") — finish or cancel it first")
					return
				}
			}
		}
	}
	// The QA squad's leader authors the suite; fall back to an agent project
	// lead. No agent → tell the human what to configure.
	author, ok := h.qaSquadLeader(r.Context(), project.WorkspaceID)
	if !ok {
		if project.LeadType.Valid && project.LeadType.String == "agent" && project.LeadID.Valid {
			if lead, lerr := h.Queries.GetAgent(r.Context(), project.LeadID); lerr == nil && !lead.ArchivedAt.Valid {
				author, ok = lead, true
			}
		}
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "no QA squad leader or agent project lead available to author the suite")
		return
	}

	requester, _ := h.parseUserUUIDOrZero(userID)
	// Status "backlog" deliberately: any other status makes IssueService.Create's
	// maybeEnqueueOnAssign fire an assignment task for the agent assignee BEFORE
	// the prompt comment below exists — the agent would start with no contract,
	// and the mention task would then run it a SECOND time. Backlog is the one
	// status the auto-enqueue skips; the mention task is the sole trigger.
	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID:  project.WorkspaceID,
		Title:        "QA base suite — golden paths (" + project.Title + ")",
		Description:  strToText("Tracking issue for authoring the project's standing golden-path regression suite. Cases captured here are promoted into the project base suite when this issue is done."),
		Status:       "backlog",
		Priority:     "high",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   author.ID,
		CreatorType:  "member",
		CreatorID:    requester,
		ProjectID:    project.ID,
	}, service.IssueCreateOpts{ActorID: userID})
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to create tracking issue: "+err.Error())
		return
	}
	// Stamp the tracking issue on the project (key-scoped) — the idempotency
	// guard above reads it on the next call.
	if stampJSON, jerr := json.Marshal(util.UUIDToString(res.Issue.ID)); jerr == nil {
		if _, serr := h.Queries.SetProjectSettingKey(r.Context(), db.SetProjectSettingKeyParams{
			ID: project.ID, WorkspaceID: project.WorkspaceID,
			Key: "base_suite_issue_id", Value: stampJSON,
		}); serr != nil {
			slog.Warn("base-suite: tracking-issue stamp failed", "project_id", util.UUIDToString(project.ID), "error", serr)
		}
	}
	comment, cerr := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID: res.Issue.ID, WorkspaceID: project.WorkspaceID,
		AuthorType: "agent", AuthorID: author.ID,
		// Inject the project QA manifest (routes/auth/flows) + docs — the prompt
		// tells the author to work FROM "the PROJECT QA MANIFEST in your
		// context", so it must actually be here or the compiled scripts have no
		// base_url/auth/routes to build on (same drop-bug fixed for run_qa).
		Content: baseSuitePromptTmpl + soloAutomationDirective +
			h.sliceActionQAManifestContext(r.Context(), res.Issue) +
			h.sliceActionQADocsContext(r.Context(), res.Issue),
		Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if cerr != nil {
		writeError(w, http.StatusBadGateway, "failed to post authoring prompt: "+cerr.Error())
		return
	}
	if _, err := h.TaskService.EnqueueTaskForMention(r.Context(), res.Issue, author.ID, comment.ID); err != nil {
		writeError(w, http.StatusBadGateway, "failed to enqueue authoring run: "+err.Error())
		return
	}
	slog.Info("base-suite authoring enqueued",
		"project_id", util.UUIDToString(project.ID), "issue_id", util.UUIDToString(res.Issue.ID),
		"agent_id", util.UUIDToString(author.ID))
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "issue_id": util.UUIDToString(res.Issue.ID)})
}
