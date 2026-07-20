package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Generic, config-driven deploy-triggered regression. Any project of the
// "deployed app + standing base suite" type opts in via project settings
// (`deploy_regression_enabled` + `qa_smoke_url`) and a run-only autopilot bound
// to a webhook trigger. On a deploy/push webhook the project's whole base suite
// runs against qa_smoke_url as a regression — "did the shipped commit break any
// existing feature?" — with no per-project code. sd-bridge is the first tenant,
// but nothing here is sd-bridge-specific.

// assembleRegressionPayload gathers a project's run-only QA contract — the
// standing base suite (embedded verbatim, since an issue-less run gets no
// per-issue slice injection), the QA target (qa_smoke_url), the results anchor
// (base-suite tracking issue, else the first task), and the primary repo — into
// a regression payload. Shared by sprint-end regression (branch=sprint branch,
// baseline=sprint-root) and generic deploy regression (branch=deployed branch,
// baseline=deployed). buildIssueDescription renders this into the agent's
// instruction with the cases embedded.
func (h *Handler) assembleRegressionPayload(ctx context.Context, projectID, wsID pgtype.UUID, branch, baseline, sprintID, directive string, tasks []sprintRegressionTask) ([]byte, error) {
	var qaTarget, repoURL, resultsIssue string
	var cases []sprintRegressionCase
	if project, perr := h.Queries.GetProject(ctx, projectID); perr == nil {
		var ps struct {
			QASmokeURL string `json:"qa_smoke_url"`
			BaseSuite  string `json:"base_suite_issue_id"`
		}
		if len(project.Settings) > 0 {
			_ = json.Unmarshal(project.Settings, &ps)
		}
		qaTarget = strings.TrimSpace(ps.QASmokeURL)
		if strings.TrimSpace(ps.BaseSuite) != "" {
			if bid, berr := parseUUIDErr(strings.TrimSpace(ps.BaseSuite)); berr == nil {
				if bi, ierr := h.Queries.GetIssue(ctx, bid); ierr == nil {
					resultsIssue = fmt.Sprintf("%s-%d", h.getIssuePrefix(ctx, wsID), bi.Number)
				}
			}
		}
	}
	for _, r := range h.listProjectResourcesForProject(ctx, projectID) {
		if r.ResourceType != "github_repo" {
			continue
		}
		var ref struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(r.ResourceRef, &ref) == nil && strings.TrimSpace(ref.URL) != "" {
			repoURL = strings.TrimSpace(ref.URL)
			break
		}
	}
	if baseCases, cerr := h.Queries.ListAutomatedTestCasesForProject(ctx, db.ListAutomatedTestCasesForProjectParams{
		ProjectID: projectID, WorkspaceID: wsID,
	}); cerr == nil {
		for _, c := range baseCases {
			cases = append(cases, sprintRegressionCase{
				ID: uuidToString(c.ID), Title: c.Title, Script: c.Script,
			})
		}
	}
	if resultsIssue == "" && len(tasks) > 0 {
		resultsIssue = tasks[0].Key
	}

	dir := strings.TrimSpace(directive)
	if dir == "" {
		dir = strings.TrimSpace(qaBaselineGuidanceFor("regression"))
	}
	return json.Marshal(sprintRegressionPayload{
		Scope: "regression", Branch: branch, Baseline: baseline, SprintID: sprintID,
		Tasks:        tasks,
		Directive:    dir,
		QATarget:     qaTarget,
		RepoURL:      repoURL,
		ResultsIssue: resultsIssue,
		Cases:        cases,
	})
}

// deployRegressionDirective steers the agent to treat a deploy-triggered run as
// a post-deploy regression (run the whole base suite against the deployed app),
// NOT a sprint-branch diff — and to emit the machine-readable results block so
// per-case results (which feature broke) reach the QA cockpit, not just a prose
// verdict.
const deployRegressionDirective = "This is a POST-DEPLOY regression, not a sprint regression. The commit just shipped to the deployed app (the QA target below). Do NOT compute a sprint-root baseline or diff branches — the baseline is \"every feature worked before this deploy\". Run EVERY case in the embedded base suite against the QA target via the agora-qa MCP `run_case_script` tool (verdict = process exit code; passed == exit 0). Then post a machine-readable ```test-runs``` block on the results issue whose body is ONLY a JSON array, one entry per case: {\"test_case_id\": \"<the embedded case id, verbatim>\", \"status\": \"pass\"|\"fail\", \"output\": \"<short evidence>\"}. Use the key `test_case_id` (NOT `id`) with the exact case id from the embedded suite — CaptureTestRuns matches on it; a wrong or missing key drops the row. This block is REQUIRED — a prose summary alone loses per-feature granularity. Verdict: qa:pass only if ALL cases pass; on any failure, qa:fail and name each failing case — that case IS the feature the new commit broke."

// projectRegressionEligible reports whether a webhook-triggered autopilot should
// run its project's base suite as a deploy regression. Generic gate (no project
// hardcoding): run-only mode, project opted in (settings.deploy_regression_enabled),
// a QA target (qa_smoke_url) to drive, and at least one automated base case to
// run. Any of these missing → fall back to the raw webhook envelope dispatch.
func (h *Handler) projectRegressionEligible(ctx context.Context, ap db.Autopilot) bool {
	if ap.ExecutionMode != "run_only" || !ap.ProjectID.Valid {
		return false
	}
	project, err := h.Queries.GetProject(ctx, ap.ProjectID)
	if err != nil {
		return false
	}
	var ps struct {
		QASmokeURL              string `json:"qa_smoke_url"`
		DeployRegressionEnabled bool   `json:"deploy_regression_enabled"`
	}
	if len(project.Settings) > 0 {
		_ = json.Unmarshal(project.Settings, &ps)
	}
	if !ps.DeployRegressionEnabled || strings.TrimSpace(ps.QASmokeURL) == "" {
		return false
	}
	cases, err := h.Queries.ListAutomatedTestCasesForProject(ctx, db.ListAutomatedTestCasesForProjectParams{
		ProjectID: ap.ProjectID, WorkspaceID: ap.WorkspaceID,
	})
	return err == nil && len(cases) > 0
}

// buildProjectRegressionPayload builds the deploy-triggered regression payload
// for a project: its base suite against qa_smoke_url, labelled with the branch +
// short SHA that just shipped (from the deploy/push webhook) so the run and its
// evidence say what was tested.
func (h *Handler) buildProjectRegressionPayload(ctx context.Context, ap db.Autopilot, deployedBranch, deployedSHA string) ([]byte, error) {
	branch := strings.TrimSpace(deployedBranch)
	if branch == "" {
		branch = "deploy"
	}
	baseline := "deployed"
	if s := strings.TrimSpace(deployedSHA); s != "" {
		if len(s) > 12 {
			s = s[:12]
		}
		baseline = "deployed@" + s
	}
	return h.assembleRegressionPayload(ctx, ap.ProjectID, ap.WorkspaceID, branch, baseline, "", deployRegressionDirective, nil)
}

// gitlabDeployRef is the subset of a GitLab Push / Deploy / Pipeline webhook body
// we read to label a deploy regression.
type gitlabDeployRef struct {
	ObjectKind  string `json:"object_kind"`
	Ref         string `json:"ref"`
	After       string `json:"after"`
	CheckoutSHA string `json:"checkout_sha"`
	// Deploy hook shape.
	Environment string `json:"environment"`
	SHA         string `json:"sha"`
}

// deployedRefFromEnvelope extracts the branch + commit SHA that shipped from a
// GitLab webhook envelope (push, deploy, or pipeline). Best-effort: unknown
// shapes yield empty strings, and the regression still runs (labelled "deploy").
func deployedRefFromEnvelope(env WebhookEnvelope) (branch, sha string) {
	var p gitlabDeployRef
	_ = json.Unmarshal(env.EventPayload, &p)
	branch = strings.TrimPrefix(strings.TrimSpace(p.Ref), "refs/heads/")
	switch {
	case strings.TrimSpace(p.After) != "":
		sha = strings.TrimSpace(p.After)
	case strings.TrimSpace(p.CheckoutSHA) != "":
		sha = strings.TrimSpace(p.CheckoutSHA)
	default:
		sha = strings.TrimSpace(p.SHA)
	}
	return branch, sha
}
