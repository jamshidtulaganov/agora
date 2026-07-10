package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Deploy slice-action support (MCP-P1, docs/deploy-mcp-integration.md §3, §5-6).
//
// A project declares its deploy targets as an ordered list in
// project.settings.deploy_environments — the same zero-migration JSONB-key
// pattern as qa_smoke_url / docs_repo (deploy-stage-research.md §3.1). Each
// entry carries only NON-SECRET routing info; the GitLab PAT the pipeline
// tools authenticate with lives in git_credential (provider='gitlab'), sealed
// at rest, and is injected into the deploy agent's MCP config at claim time
// (gitlab_mcp.go). Splitting routing from secrets means rotating a token never
// touches project settings and repointing an environment never touches a
// secret (deploy-mcp-integration.md §3).
//
// Example settings value:
//
//	"deploy_environments": [
//	  {"key": "staging", "label": "Staging", "kind": "gitlab_pipeline",
//	   "target": {"project_path": "salesdoctor/sd-main", "ref": "staging",
//	              "environment": "staging"}},
//	  {"key": "production", "label": "Production", "kind": "gitlab_pipeline",
//	   "target": {"project_path": "salesdoctor/sd-main", "ref": "main"},
//	   "requires_human": true}
//	]

// deployEnvironmentTarget is the non-secret machine half of one configured
// deploy environment. For kind="gitlab_pipeline" the project_path + ref name
// the pipeline to trigger; environment optionally names the GitLab
// Environments entry the pipeline deploys (informational). command is the
// Tier-2 fallback (deploy-stage-research.md §3.2): a shell one-liner the
// agent runs on its daemon when no pipeline target is configured — or when
// the MCP server is unreachable at run time (deploy-mcp-integration.md §7).
type deployEnvironmentTarget struct {
	Kind        string `json:"kind"`
	ProjectPath string `json:"project_path"`
	Ref         string `json:"ref"`
	Environment string `json:"environment"`
	Command     string `json:"command"`
}

// deployEnvironment is one entry of project.settings.deploy_environments.
// kind may live on the environment (the documented shape) or inside target
// (tolerated) — targetKind() resolves the precedence.
type deployEnvironment struct {
	Key           string                  `json:"key"`
	Label         string                  `json:"label"`
	Kind          string                  `json:"kind"`
	RequiresHuman bool                    `json:"requires_human"`
	Target        deployEnvironmentTarget `json:"target"`
}

// targetKind resolves the environment's target kind: the env-level kind wins,
// falling back to target.kind so both authoring shapes parse.
func (e deployEnvironment) targetKind() string {
	if k := strings.TrimSpace(e.Kind); k != "" {
		return k
	}
	return strings.TrimSpace(e.Target.Kind)
}

// parseDeployEnvironments reads project.settings.deploy_environments
// defensively: a malformed settings blob or a non-array value yields nil, a
// malformed ENTRY is skipped (one bad entry must not hide its siblings), and
// entries without a key are dropped (the key is the routing handle the Deploy
// button and the slice-action scope address).
func parseDeployEnvironments(settingsRaw []byte) []deployEnvironment {
	if len(settingsRaw) == 0 {
		return nil
	}
	var settings struct {
		DeployEnvironments []json.RawMessage `json:"deploy_environments"`
	}
	if json.Unmarshal(settingsRaw, &settings) != nil {
		return nil
	}
	out := make([]deployEnvironment, 0, len(settings.DeployEnvironments))
	for _, raw := range settings.DeployEnvironments {
		var env deployEnvironment
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		if strings.TrimSpace(env.Key) == "" {
			continue
		}
		out = append(out, env)
	}
	return out
}

// findDeployEnvironment matches an environment by key, case-insensitively.
func findDeployEnvironment(envs []deployEnvironment, key string) (deployEnvironment, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return deployEnvironment{}, false
	}
	for _, env := range envs {
		if strings.ToLower(strings.TrimSpace(env.Key)) == key {
			return env, true
		}
	}
	return deployEnvironment{}, false
}

// deployRefRe is the allowlist for a caller-supplied deploy ref override
// (CreateSliceActionRequest.Ref — the sprint Deploy panel passes the sprint
// branch). Git-ref-shaped only: no backticks (the ref is embedded inside a
// `…` code span in the rendered instruction), no whitespace/newlines, no
// mention-forming delimiters. Deliberately conservative — a legitimate
// branch name ("sprint/9f2c…", "billing", "release-2.4") always fits.
var deployRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// sanitizeDeployRef validates a caller-supplied ref override. Returns "" for
// anything that is not plainly a git ref — the caller then keeps the
// environment's configured target.ref instead of failing the request (the
// override is an optimization, not a contract; a miss must not block a
// deploy that would have worked without it).
func sanitizeDeployRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 200 || !deployRefRe.MatchString(ref) {
		return ""
	}
	return ref
}

// deployEnvironmentRequiresHuman reports whether firing a deploy to this
// environment is a human-only action: the explicit requires_human flag, OR a
// production-named key as defense in depth — an admin who names an
// environment "production" but forgets the flag must not silently open
// agent-triggered prod deploys (deploy-stage-research.md §3.5).
func deployEnvironmentRequiresHuman(env deployEnvironment) bool {
	if env.RequiresHuman {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(env.Key)) {
	case "production", "prod":
		return true
	}
	return false
}

// projectDeployEnvironments loads + parses the issue's project's configured
// deploy environments. nil when the issue has no project, the project has no
// settings, or nothing usable is configured.
func (h *Handler) projectDeployEnvironments(ctx context.Context, issue db.Issue) []deployEnvironment {
	if !issue.ProjectID.Valid {
		return nil
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return nil
	}
	return parseDeployEnvironments(project.Settings)
}

// resolveDeployEnvironment resolves the environment a deploy slice-action
// targets (scope = the environment key) and enforces the production human
// gate, writing the 4xx itself and returning ok=false on any miss.
//
// The gate uses IsMachineActor — RequireHumanActor's per-handler counterpart —
// because the environment is only known after decoding the request body, so a
// route-level middleware cannot gate it (see actor_guards.go). An agent (mat_
// task token) or cloud node (mcn_ PAT) may fire non-production deploys; a
// requires_human/production environment is a human-only trigger. The agent
// can PREPARE everything, but the button that fires a production deploy is a
// human's (deploy-mcp-integration.md §5).
func (h *Handler) resolveDeployEnvironment(w http.ResponseWriter, r *http.Request, issue db.Issue, key string) (deployEnvironment, bool) {
	envs := h.projectDeployEnvironments(r.Context(), issue)
	if len(envs) == 0 {
		writeError(w, http.StatusBadRequest, "this issue's project has no deploy_environments configured")
		return deployEnvironment{}, false
	}
	env, ok := findDeployEnvironment(envs, key)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown deploy environment (scope must be a configured deploy_environments key)")
		return deployEnvironment{}, false
	}
	if deployEnvironmentRequiresHuman(env) && IsMachineActor(r) {
		writeError(w, http.StatusForbidden, "deploys to a production environment require a human actor")
		return deployEnvironment{}, false
	}
	return env, true
}

// gitlabPipelineTarget parameterizes deployGitLabPipelineClause. Environment
// is OUR environment key (what deploy-result's environment field and the
// deploy_event.target column record); GitLabEnv is GitLab's own Environments
// name when configured (informational — the pipeline's .gitlab-ci.yml decides
// what actually deploys where).
type gitlabPipelineTarget struct {
	ProjectPath string
	Ref         string
	Environment string
	GitLabEnv   string
}

// deployGitLabPipelineClause instructs a deploy agent that has GitLab MCP
// tools attached (injectGitLabMcpCreds ran and found a credential) to drive a
// real CI/CD pipeline instead of running a local command. Mirrors
// qaLocalDirectoryClause's shape: names the exact tools, states the values,
// states the write-back fields, states the hard invariants
// (deploy-mcp-integration.md §6). The ref is server-computed from the
// environment's configuration and embedded as a literal — the agent is told
// which ref, never asked to infer it (§7, wrong-ref row).
func deployGitLabPipelineClause(t gitlabPipelineTarget) string {
	c := " DEPLOY TARGET = GitLab CI/CD pipeline (MCP): trigger a pipeline for GitLab project `" +
		t.ProjectPath + "` on ref `" + t.Ref + "` using the `gitlab` MCP server's `create_pipeline` tool."
	if t.GitLabEnv != "" {
		c += " The pipeline deploys the GitLab environment `" + t.GitLabEnv + "`."
	}
	c += " Poll `get_pipeline` every ~15s until its status is `success`, `failed`, or `canceled` — do NOT " +
		"poll for more than 10 minutes; if it is still `running`/`pending` past that, report " +
		"status=\"timeout\" and stop (a human or the watchdog decides what's next). On failure, call " +
		"`get_pipeline_job_output` for each failed job and include the last ~50 lines of each in your " +
		"report and in the `failed_jobs` field. NEVER call `retry_pipeline` or `cancel_pipeline` unless " +
		"explicitly asked — your job is to trigger and report, not to manage the pipeline's lifecycle. " +
		"In your ```deploy-result``` block set environment=\"" + t.Environment + "\", ref=\"" + t.Ref +
		"\", and pipeline_url to the pipeline's `web_url` from `get_pipeline`."
	return c
}

// deployCommandClause is the Tier-2 target contract: the agent runs the
// configured command on its daemon and reports strictly by exit code
// (deploy-stage-research.md §3.2).
func deployCommandClause(envKey, command, ref string) string {
	c := " DEPLOY TARGET = COMMAND: deploy by running `" + command + "` on this daemon"
	if ref != "" {
		c += " with ref `" + ref + "` checked out first"
	}
	c += ", reporting strictly by EXIT CODE — 0 is success, anything else is failed (include the last " +
		"~50 lines of output in your report). In your ```deploy-result``` block set environment=\"" + envKey + "\""
	if ref != "" {
		c += " and ref=\"" + ref + "\""
	}
	c += "."
	return c
}

// deployTargetClause renders the environment-specific contract appended to
// the deploy template: which environment, which machine target, and the exact
// write-back values. ok=false when the environment has no usable target — the
// handler 400s instead of dispatching a doomed agent run.
func deployTargetClause(env deployEnvironment) (string, bool) {
	key := strings.TrimSpace(env.Key)
	out := " DEPLOY ENVIRONMENT: `" + key + "`"
	if l := strings.TrimSpace(env.Label); l != "" && !strings.EqualFold(l, key) {
		out += " (" + l + ")"
	}
	out += " — deploy to THIS environment only."

	projectPath := strings.TrimSpace(env.Target.ProjectPath)
	ref := strings.TrimSpace(env.Target.Ref)
	command := strings.TrimSpace(env.Target.Command)

	if env.targetKind() == "gitlab_pipeline" && projectPath != "" && ref != "" {
		out += deployGitLabPipelineClause(gitlabPipelineTarget{
			ProjectPath: projectPath,
			Ref:         ref,
			Environment: key,
			GitLabEnv:   strings.TrimSpace(env.Target.Environment),
		})
		if command != "" {
			out += " FALLBACK (ONLY if the `gitlab` MCP tools are unavailable this run): deploy by running `" +
				command + "` on this daemon instead, reporting its exit code the same way."
		}
		return out, true
	}
	if command != "" {
		return out + deployCommandClause(key, command, ref), true
	}
	return "", false
}

// taskTriggerIsDeploy reports whether a claimed task was dispatched by a
// deploy slice-action: its triggering comment starts with the deploy
// agent-protocol marker (agentProtocolMarker sits at the very start of every
// slice-action comment, ahead of the @mention). The claim path uses this to
// attach GitLab pipeline MCP tools to deploy tasks ONLY.
func (h *Handler) taskTriggerIsDeploy(ctx context.Context, triggerCommentID pgtype.UUID) bool {
	if !triggerCommentID.Valid {
		return false
	}
	c, err := h.Queries.GetComment(ctx, triggerCommentID)
	if err != nil {
		return false
	}
	marker := strings.TrimSuffix(agentProtocolMarker(sliceActionDeploy), "\n")
	return strings.HasPrefix(strings.TrimSpace(c.Content), marker)
}

// projectDeployAgent resolves the project's configured deploy agent
// (project.settings.deploy_agent) — the first hop of the deploy agent
// resolution chain (deploy-mcp-integration.md §5, mirroring
// resolveAutoDocsAgent's docs_agent hop). ok=false when unset, invalid, or
// not a ready agent in this workspace; the caller then falls through to the
// issue's agent assignee / the caller's own agent.
func (h *Handler) projectDeployAgent(ctx context.Context, issue db.Issue) (db.Agent, bool) {
	if !issue.ProjectID.Valid {
		return db.Agent{}, false
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return db.Agent{}, false
	}
	var settings struct {
		DeployAgent string `json:"deploy_agent"`
	}
	if json.Unmarshal(project.Settings, &settings) != nil {
		return db.Agent{}, false
	}
	id := strings.TrimSpace(settings.DeployAgent)
	if id == "" {
		return db.Agent{}, false
	}
	aid, err := util.ParseUUID(id)
	if err != nil {
		return db.Agent{}, false
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID: aid, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil || !sliceAgentReady(agent) {
		return db.Agent{}, false
	}
	return agent, true
}
