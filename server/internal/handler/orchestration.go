package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

type orchestrationPlanRequest struct {
	// Mode is the deprecated auto/manual alias. New callers send the two
	// orthogonal fields below: who executes and how ready work advances.
	Mode              string `json:"mode"`
	ExecutionStrategy string `json:"execution_strategy"`
	ProgressionPolicy string `json:"progression_policy"`
	SquadID           string `json:"squad_id"`
	// AutoStart is a pointer so omission can inherit the project's
	// review_plan_first default while an explicit false still creates a draft.
	AutoStart *bool                      `json:"auto_start"`
	Policy    map[string]any             `json:"policy"`
	Steps     []orchestrationStepRequest `json:"steps"`
}

type projectOrchestrationDefaults struct {
	ExecutionStrategy string `json:"execution_strategy"`
	ProgressionPolicy string `json:"progression_policy"`
	MaxConcurrency    int    `json:"max_concurrency"`
	ReviewPlanFirst   *bool  `json:"review_plan_first"`
	SquadID           string `json:"-"`
}

// orchestrationDefaultsForIssue reads the optional project-level execution
// policy. Invalid or stale values fail open to the existing inferred defaults;
// project settings must never make an issue impossible to run after a squad is
// removed or an older client writes an unknown value.
func (h *Handler) orchestrationDefaultsForIssue(ctx context.Context, issue db.Issue) projectOrchestrationDefaults {
	if !issue.ProjectID.Valid {
		return projectOrchestrationDefaults{}
	}
	project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: issue.ProjectID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return projectOrchestrationDefaults{}
	}
	var settings struct {
		Orchestration projectOrchestrationDefaults `json:"orchestration"`
	}
	if len(project.Settings) == 0 || json.Unmarshal(project.Settings, &settings) != nil {
		return projectOrchestrationDefaults{}
	}
	defaults := settings.Orchestration
	defaults.ExecutionStrategy = strings.ToLower(strings.TrimSpace(defaults.ExecutionStrategy))
	if defaults.ExecutionStrategy != "automatic" && !validExecutionStrategy(defaults.ExecutionStrategy) {
		defaults.ExecutionStrategy = ""
	}
	// A custom DAG cannot be a project default because it requires request-time
	// steps. Treat a drifted custom value as automatic inference.
	if defaults.ExecutionStrategy == "custom" {
		defaults.ExecutionStrategy = ""
	}
	defaults.ProgressionPolicy = normalizeProgressionPolicy(defaults.ProgressionPolicy)
	if defaults.MaxConcurrency < 1 || defaults.MaxConcurrency > 10 {
		defaults.MaxConcurrency = 0
	}
	if project.SquadID.Valid {
		defaults.SquadID = uuidToString(project.SquadID)
	}
	return defaults
}

type orchestrationStepRequest struct {
	Key              string `json:"key"`
	Title            string `json:"title"`
	Stage            string `json:"stage"`
	AgentID          string `json:"agent_id"`
	Model            string `json:"model"`
	Instructions     string `json:"instructions"`
	ApprovalRequired bool   `json:"approval_required"`
	// HumanOnly marks an approval checkpoint that completes when approved. It
	// must not inherit the run controller and dispatch an agent afterwards.
	HumanOnly        bool     `json:"human_only"`
	MaxAttempts      int32    `json:"max_attempts"`
	DependsOnKeys    []string `json:"depends_on_keys"`
	ParentKey        string   `json:"parent_key"`
	SquadID          string   `json:"squad_id"`
	Kind             string   `json:"kind"`
	Capability       string   `json:"capability"`
	DependsOnStepIDs []string `json:"depends_on_step_ids"`
}

type orchestrationRunResponse struct {
	ID                string                          `json:"id"`
	IssueID           string                          `json:"issue_id"`
	Status            string                          `json:"status"`
	Mode              string                          `json:"mode"`
	ExecutionStrategy string                          `json:"execution_strategy"`
	ProgressionPolicy string                          `json:"progression_policy"`
	Policy            json.RawMessage                 `json:"policy"`
	OwnerType         string                          `json:"owner_type"`
	OwnerID           string                          `json:"owner_id,omitempty"`
	ControllerAgentID string                          `json:"controller_agent_id,omitempty"`
	BaseGitStates     json.RawMessage                 `json:"base_git_states"`
	ExecutionMode     string                          `json:"execution_mode"`
	PlanVersion       int32                           `json:"plan_version"`
	Revisions         []orchestrationRevisionResponse `json:"revisions"`
	StartedAt         any                             `json:"started_at,omitempty"`
	CompletedAt       any                             `json:"completed_at,omitempty"`
	CreatedAt         any                             `json:"created_at"`
	UpdatedAt         any                             `json:"updated_at"`
	Steps             []orchestrationStepResponse     `json:"steps"`
	Events            []orchestrationEventResponse    `json:"events"`
	Messages          []orchestrationMessageResponse  `json:"messages"`
}

type orchestrationRevisionResponse struct {
	ID        string          `json:"id"`
	Version   int32           `json:"version"`
	ActorType string          `json:"actor_type"`
	ActorID   string          `json:"actor_id,omitempty"`
	Reason    string          `json:"reason"`
	Patch     json.RawMessage `json:"patch"`
	CreatedAt any             `json:"created_at"`
}

type orchestrationPlanEditRequest struct {
	ExpectedVersion int32                     `json:"expected_version"`
	Reason          string                    `json:"reason"`
	Operation       string                    `json:"operation"`
	StepID          string                    `json:"step_id"`
	AgentID         string                    `json:"agent_id"`
	Model           string                    `json:"model"`
	Instructions    string                    `json:"instructions"`
	Child           *orchestrationStepRequest `json:"child"`
}

type orchestrationRouting struct {
	OwnerType          string
	OwnerID            pgtype.UUID
	ControllerAgent    pgtype.UUID
	DevelopmentAgent   pgtype.UUID
	ExecutionMode      string
	ExplicitController bool
}

type orchestrationPlannerMember struct {
	AgentID            pgtype.UUID
	Name               string
	Role               string
	Description        string
	Instructions       string
	IsLeader           bool
	Model              string
	ThinkingLevel      string
	MaxConcurrentTasks int32
}

type orchestrationStepResponse struct {
	ID                 string          `json:"id"`
	Key                string          `json:"key"`
	Title              string          `json:"title"`
	Stage              string          `json:"stage"`
	Status             string          `json:"status"`
	Position           int32           `json:"position"`
	AgentID            string          `json:"agent_id,omitempty"`
	Model              string          `json:"model,omitempty"`
	ThinkingLevel      *string         `json:"thinking_level,omitempty"`
	TaskID             string          `json:"task_id,omitempty"`
	QuestionID         string          `json:"question_id,omitempty"`
	ApprovalRequired   bool            `json:"approval_required"`
	ApprovedBy         string          `json:"approved_by,omitempty"`
	Attempt            int32           `json:"attempt"`
	MaxAttempts        int32           `json:"max_attempts"`
	Instructions       string          `json:"instructions"`
	Output             json.RawMessage `json:"output,omitempty"`
	Error              string          `json:"error,omitempty"`
	DependsOnStepIDs   []string        `json:"depends_on_step_ids"`
	ParentStepID       string          `json:"parent_step_id,omitempty"`
	SquadID            string          `json:"squad_id,omitempty"`
	ControllerAgentID  string          `json:"controller_agent_id,omitempty"`
	WorktreeBranch     string          `json:"worktree_branch,omitempty"`
	BaseSHA            string          `json:"base_sha,omitempty"`
	HeadSHA            string          `json:"head_sha,omitempty"`
	MergeStatus        string          `json:"merge_status"`
	ConflictFiles      json.RawMessage `json:"conflict_files"`
	Kind               string          `json:"kind"`
	Capability         string          `json:"capability"`
	IntegrationStatus  string          `json:"integration_status"`
	IntegratedHeadSHAs json.RawMessage `json:"integrated_head_shas"`
	MissingHeadSHAs    json.RawMessage `json:"missing_head_shas"`
}

func (h *Handler) resolveSquadStep(ctx context.Context, workspaceID pgtype.UUID, input orchestrationStepRequest) (pgtype.UUID, pgtype.UUID, error) {
	squadID, err := parseOptionalUUID(input.SquadID)
	if err != nil || !squadID.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: squadID, WorkspaceID: workspaceID})
	if err != nil || !squad.LeaderID.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("squad or squad leader not found")
	}
	return squadID, squad.LeaderID, nil
}

type orchestrationEventResponse struct {
	ID        string          `json:"id"`
	StepID    string          `json:"step_id,omitempty"`
	Kind      string          `json:"kind"`
	ActorType string          `json:"actor_type"`
	ActorID   string          `json:"actor_id,omitempty"`
	Details   json.RawMessage `json:"details"`
	CreatedAt any             `json:"created_at"`
}

type orchestrationMessageResponse struct {
	ID             string          `json:"id"`
	StepID         string          `json:"step_id"`
	Kind           string          `json:"kind"`
	ActorType      string          `json:"actor_type"`
	ActorID        string          `json:"actor_id,omitempty"`
	TargetType     string          `json:"target_type"`
	TargetID       string          `json:"target_id,omitempty"`
	Body           json.RawMessage `json:"body"`
	PlanVersion    int32           `json:"plan_version"`
	CorrelationID  string          `json:"correlation_id"`
	CausationID    string          `json:"causation_id,omitempty"`
	ReplyToID      string          `json:"reply_to_id,omitempty"`
	ExpectsReply   bool            `json:"expects_reply"`
	AcknowledgedAt any             `json:"acknowledged_at,omitempty"`
	ResolvedAt     any             `json:"resolved_at,omitempty"`
	CreatedAt      any             `json:"created_at"`
}

func validOrchestrationStage(stage string) bool {
	switch stage {
	case "plan", "dev", "qa", "review", "release":
		return true
	default:
		return false
	}
}

// orchestrationOwnsIssuePipeline reports whether a started run is the sole
// dispatcher for this issue. Legacy status/label reflexes (auto-QA, auto-review,
// QA-fail rerouting, docs and merge routing) must stand down while this is true;
// the persisted DAG already contains those stages and duplicate side tasks can
// contend for the same local directory or certify the wrong commit.
func (h *Handler) orchestrationOwnsIssuePipeline(ctx context.Context, issueID pgtype.UUID) bool {
	run, err := h.Queries.GetActiveOrchestrationRunForIssue(ctx, issueID)
	if err != nil {
		return false
	}
	return run.Status == "running" || run.Status == "waiting_approval" ||
		run.Status == "waiting_input" || run.Status == "blocked"
}

// cancelActiveOrchestrationForIssue closes the persisted DAG before a bulk
// task cancellation. CancelTasksForIssue updates task rows directly and does
// not invoke orchestration terminal callbacks, so cancelling tasks first would
// leave queued/running steps and the run permanently active.
func (h *Handler) cancelActiveOrchestrationForIssue(ctx context.Context, issueID pgtype.UUID, actorType string, actorID pgtype.UUID) bool {
	run, err := h.Queries.GetActiveOrchestrationRunForIssue(ctx, issueID)
	if err != nil {
		return false
	}
	steps, err := h.Queries.ListOrchestrationSteps(ctx, run.ID)
	if err != nil {
		return false
	}
	for _, step := range steps {
		cancelled, cancelErr := h.Queries.CancelOrchestrationStep(ctx, step.ID)
		if cancelErr != nil {
			continue
		}
		h.createOrchestrationEvent(ctx, run.ID, cancelled.ID, "step_cancelled", actorType, actorID, map[string]any{"reason": "issue_cancelled"})
	}
	if _, err := h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"}); err != nil {
		return false
	}
	h.createOrchestrationEvent(ctx, run.ID, pgtype.UUID{}, "run_cancelled", actorType, actorID, map[string]any{"reason": "issue_cancelled"})
	return true
}

func validExecutionStrategy(strategy string) bool {
	switch strategy {
	case "human", "solo", "squad", "custom":
		return true
	default:
		return false
	}
}

func validOrchestrationCapability(capability string) bool {
	switch capability {
	case "coordination", "implementation", "backend", "frontend", "mobile", "infrastructure", "documentation", "integration", "qa", "review", "release":
		return true
	default:
		return false
	}
}

func inferredStepCapability(step orchestrationStepRequest) string {
	if step.Kind == "integration" {
		return "integration"
	}
	switch step.Stage {
	case "plan":
		return "coordination"
	case "qa":
		return "qa"
	case "review":
		return "review"
	case "release":
		return "release"
	default:
		return "implementation"
	}
}

// prepareOrchestrationPlan normalizes and validates planner output before a
// run row is created. Dependencies must point backward, which proves the graph
// is acyclic. Multiple terminal development branches must converge through an
// integration step before QA/review can consume the result.
func prepareOrchestrationPlan(steps []orchestrationStepRequest) error {
	seen := make(map[string]int, len(steps))
	devTasks := make(map[string]bool)
	devNonLeaves := make(map[string]bool)
	for index := range steps {
		step := &steps[index]
		step.Key = strings.TrimSpace(step.Key)
		step.Title = strings.TrimSpace(step.Title)
		step.Stage = strings.TrimSpace(step.Stage)
		step.Kind = strings.TrimSpace(step.Kind)
		if step.Kind == "" {
			step.Kind = "task"
		}
		step.Capability = strings.TrimSpace(step.Capability)
		if step.Capability == "" {
			step.Capability = inferredStepCapability(*step)
		}
		if step.Key == "" || step.Title == "" || !validOrchestrationStage(step.Stage) {
			return fmt.Errorf("every step requires a unique key, title, and valid stage")
		}
		if _, duplicate := seen[step.Key]; duplicate {
			return fmt.Errorf("step key %q is duplicated", step.Key)
		}
		if step.Kind != "task" && step.Kind != "integration" {
			return fmt.Errorf("step %q has invalid kind %q", step.Key, step.Kind)
		}
		if !validOrchestrationCapability(step.Capability) {
			return fmt.Errorf("step %q has invalid capability %q", step.Key, step.Capability)
		}
		if step.Kind == "integration" && step.Capability != "integration" {
			return fmt.Errorf("integration step %q must use integration capability", step.Key)
		}
		if step.Kind == "integration" && len(step.DependsOnKeys) == 0 {
			return fmt.Errorf("integration step %q requires at least one dependency", step.Key)
		}
		for dependencyIndex, dependencyKey := range step.DependsOnKeys {
			dependencyKey = strings.TrimSpace(dependencyKey)
			step.DependsOnKeys[dependencyIndex] = dependencyKey
			if _, exists := seen[dependencyKey]; !exists {
				return fmt.Errorf("step %q dependency %q must reference an earlier step", step.Key, dependencyKey)
			}
			if step.Stage == "dev" && step.Kind == "task" && devTasks[dependencyKey] {
				devNonLeaves[dependencyKey] = true
			}
		}
		if parent := strings.TrimSpace(step.ParentKey); parent != "" {
			step.ParentKey = parent
			if _, exists := seen[parent]; !exists {
				return fmt.Errorf("step %q parent %q must reference an earlier step", step.Key, parent)
			}
		}
		seen[step.Key] = index
		if step.Stage == "dev" && step.Kind == "task" {
			devTasks[step.Key] = true
		}
	}

	var terminalDev []string
	for key := range devTasks {
		if !devNonLeaves[key] {
			terminalDev = append(terminalDev, key)
		}
	}
	for _, step := range steps {
		if step.Stage != "qa" && step.Stage != "review" {
			continue
		}
		hasIntegrationArtifact := false
		hasDirectDevelopmentArtifact := false
		for _, dependencyKey := range step.DependsOnKeys {
			dependency := steps[seen[dependencyKey]]
			hasIntegrationArtifact = hasIntegrationArtifact || dependency.Kind == "integration"
			hasDirectDevelopmentArtifact = hasDirectDevelopmentArtifact ||
				(dependency.Stage == "dev" && dependency.Kind == "task")
		}
		if hasIntegrationArtifact && hasDirectDevelopmentArtifact {
			return fmt.Errorf("%s step %q cannot mix an integration artifact with direct development artifacts", step.Stage, step.Key)
		}
	}
	if len(terminalDev) < 2 {
		return nil
	}
	sort.Strings(terminalDev)
	joiningIntegrations := make(map[string]bool)
	for _, step := range steps {
		if step.Kind != "integration" {
			continue
		}
		dependencies := make(map[string]bool, len(step.DependsOnKeys))
		for _, key := range step.DependsOnKeys {
			dependencies[key] = true
		}
		joinsAll := true
		for _, key := range terminalDev {
			if !dependencies[key] {
				joinsAll = false
				break
			}
		}
		if joinsAll {
			joiningIntegrations[step.Key] = true
		}
	}
	if len(joiningIntegrations) == 0 {
		return fmt.Errorf("parallel development branches %s require an integration join", strings.Join(terminalDev, ", "))
	}
	for _, step := range steps {
		if step.Stage != "qa" && step.Stage != "review" {
			continue
		}
		consumesJoin := false
		for _, dependency := range step.DependsOnKeys {
			if joiningIntegrations[dependency] {
				consumesJoin = true
				break
			}
		}
		if !consumesJoin {
			return fmt.Errorf("%s step %q must depend on the integration join", step.Stage, step.Key)
		}
	}
	return nil
}

func normalizeProgressionPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "automatic":
		return "automatic"
	case "gated", "approval_gated":
		return "gated"
	case "manual":
		return "manual"
	default:
		return ""
	}
}

func legacyOrchestrationMode(policy string) string {
	if policy == "manual" {
		return "manual"
	}
	return "auto"
}

func legacyExecutionMode(strategy string) string {
	switch strategy {
	case "solo", "human":
		return "direct"
	case "squad":
		return "squad"
	default:
		return "orchestrated"
	}
}

func metadataAgentID(issue db.Issue, key string) pgtype.UUID {
	var metadata map[string]any
	if json.Unmarshal(issue.Metadata, &metadata) != nil {
		return pgtype.UUID{}
	}
	value, _ := metadata[key].(string)
	id, err := parseOptionalUUID(value)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}

func parseOptionalUUID(value string) (pgtype.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}, nil
	}
	return parseUUIDValue(value)
}

func parseUUIDValue(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID")
	}
	return id, nil
}

func (h *Handler) orchestrationRouting(ctx context.Context, issue db.Issue) orchestrationRouting {
	routing := orchestrationRouting{OwnerType: "unassigned", ExecutionMode: "orchestrated"}
	if issue.AssigneeType.Valid {
		routing.OwnerType = issue.AssigneeType.String
		routing.OwnerID = issue.AssigneeID
	}
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" {
		routing.DevelopmentAgent = issue.AssigneeID
		routing.ExecutionMode = "direct"
	}
	if controller, ok := h.orchestratorForIssue(ctx, issue); ok {
		routing.ControllerAgent = controller.ID
		if issue.AssigneeType.String == "squad" {
			routing.DevelopmentAgent = controller.ID
			routing.ExecutionMode = "squad"
		} else if routing.DevelopmentAgent != controller.ID {
			routing.ExecutionMode = "orchestrated"
		}
	}
	// An explicitly cast orchestrator wins over the derived squad/solo owner.
	if explicit := metadataAgentID(issue, "orchestrator_agent_id"); explicit.Valid {
		routing.ControllerAgent = explicit
		routing.ExecutionMode = "orchestrated"
		routing.ExplicitController = true
	}
	return routing
}

func inferExecutionStrategy(routing orchestrationRouting, customPlan bool) string {
	if customPlan {
		return "custom"
	}
	// Strategy follows the issue assignee. An agent assignee owns one persisted
	// solo work step; a squad assignee fans out through the persisted squad DAG.
	switch routing.OwnerType {
	case "member", "unassigned":
		return "human"
	case "squad":
		return "squad"
	case "agent":
		return "solo"
	default:
		return "solo"
	}
}

// resolveExecutionStrategy picks the run shape when the request omits
// execution_strategy. Agent/squad assignees win over a conflicting project
// pin so one-click "Start execution plan" stays assignee-correct. Project
// defaults control progression/review/concurrency, not ownership topology;
// Customize (an explicit request strategy) can still override.
func resolveExecutionStrategy(routing orchestrationRouting, projectStrategy string, customPlan bool) string {
	_ = projectStrategy // compatibility read; no longer an ownership input
	if customPlan {
		return "custom"
	}
	return inferExecutionStrategy(routing, false)
}

// applySoloAgentRouting keeps a solo run owned by the assigned agent. An
// agent that happens to belong to a squad still has that squad's leader as
// the issue-level orchestrator for QA-fail routing, but a solo execution
// plan must not hand planning/release to that leader's roster.
func applySoloAgentRouting(routing orchestrationRouting) orchestrationRouting {
	if routing.OwnerType != "agent" || !routing.OwnerID.Valid {
		return routing
	}
	agentID := routing.DevelopmentAgent
	if !agentID.Valid {
		agentID = routing.OwnerID
	}
	routing.DevelopmentAgent = agentID
	if !routing.ExplicitController {
		routing.ControllerAgent = agentID
		routing.ExecutionMode = "direct"
	}
	return routing
}

func progressionPolicyForIssue(issue db.Issue, requested, legacyMode, projectDefault string) string {
	if normalized := normalizeProgressionPolicy(requested); normalized != "" {
		return normalized
	}
	if normalized := normalizeProgressionPolicy(legacyMode); normalized != "" {
		return normalized
	}
	if normalized := normalizeProgressionPolicy(issueMetadataString(issue.Metadata, "progression_policy")); normalized != "" {
		return normalized
	}
	// Compatibility read only. New UI and writes use progression_policy.
	if normalized := normalizeProgressionPolicy(issueMetadataString(issue.Metadata, "pipeline_mode")); normalized != "" {
		return normalized
	}
	if normalized := normalizeProgressionPolicy(projectDefault); normalized != "" {
		return normalized
	}
	return "automatic"
}

const (
	manualDispatchAuthorizationPolicyKey = "manual_dispatch_authorized"
	maxOrchestrationClarificationRounds  = 3
)

func manualOrchestrationDispatchAuthorized(run db.OrchestrationRun) bool {
	if run.ProgressionPolicy != "manual" {
		return true
	}
	var policy map[string]any
	if json.Unmarshal(run.Policy, &policy) != nil {
		return false
	}
	authorized, _ := policy[manualDispatchAuthorizationPolicyKey].(bool)
	return authorized
}

func setManualOrchestrationDispatchAuthorization(
	ctx context.Context,
	queries *db.Queries,
	run db.OrchestrationRun,
	authorized bool,
) (db.OrchestrationRun, error) {
	if run.ProgressionPolicy != "manual" {
		return run, nil
	}
	return queries.SetOrchestrationManualDispatchAuthorization(ctx, db.SetOrchestrationManualDispatchAuthorizationParams{
		ID: run.ID, Authorized: authorized,
	})
}

func defaultOrchestrationSteps(issue db.Issue, routing orchestrationRouting, strategy string) []orchestrationStepRequest {
	return defaultOrchestrationStepsWithMembers(issue, routing, strategy, nil, 3)
}

func plannerCapability(member orchestrationPlannerMember) string {
	text := strings.ToLower(strings.Join([]string{member.Role, member.Name, member.Description}, " "))
	switch {
	case member.IsLeader || strings.TrimSpace(strings.ToLower(member.Role)) == "leader":
		return "coordination"
	case strings.Contains(text, "fullstack") || strings.Contains(text, "full-stack") || strings.Contains(text, "generalist"):
		return "implementation"
	case strings.Contains(text, "qa") || strings.Contains(text, "quality") || strings.Contains(text, "test"):
		return "qa"
	case strings.Contains(text, "review") || strings.Contains(text, "security") || strings.Contains(text, "audit"):
		return "review"
	case strings.Contains(text, "frontend") || strings.Contains(text, "front-end") || strings.Contains(text, "web ui") || strings.Contains(text, "react"):
		return "frontend"
	case strings.Contains(text, "backend") || strings.Contains(text, "back-end") || strings.Contains(text, "api") || strings.Contains(text, "database") || strings.Contains(text, "server"):
		return "backend"
	case strings.Contains(text, "mobile") || strings.Contains(text, "ios") || strings.Contains(text, "android") || strings.Contains(text, "react native"):
		return "mobile"
	case strings.Contains(text, "infra") || strings.Contains(text, "devops") || strings.Contains(text, "platform") || strings.Contains(text, "release"):
		return "infrastructure"
	case strings.Contains(text, "docs") || strings.Contains(text, "documentation") || strings.Contains(text, "technical writer"):
		return "documentation"
	default:
		return "implementation"
	}
}

// plannerMemberCanPerform keeps a squad label from overriding an agent's
// durable operating contract. Squad roles are useful routing hints, but an
// agent instruction that explicitly forbids implementation is authoritative.
// Without this guard a knowledge-capture bot can be labelled "implementation"
// and produce a successful no-op branch that silently reaches integration.
func plannerMemberCanPerform(member orchestrationPlannerMember, capability string) bool {
	switch capability {
	case "implementation", "backend", "frontend", "mobile", "infrastructure", "documentation":
	default:
		return true
	}
	instructions := strings.ToLower(member.Instructions)
	for _, phrase := range []string{
		"never start work beyond",
		"never implement",
		"do not implement",
		"must not implement",
		"forbids implementation",
		"only perform knowledge",
		"knowledge synthesizer",
	} {
		if strings.Contains(instructions, phrase) {
			return false
		}
	}
	return true
}

// orchestrationCapabilityCompatible validates a proposed route against the
// worker's durable squad role. The controller may cover any capability as the
// recovery owner; generalists may cover any implementation specialty. Other
// workers stay inside their inferred responsibility so a frontend branch
// cannot silently be rerouted to a QA-only or knowledge-only agent.
func orchestrationCapabilityCompatible(member orchestrationPlannerMember, capability string) bool {
	if !plannerMemberCanPerform(member, capability) {
		return false
	}
	if member.IsLeader {
		return true
	}
	inferred := plannerCapability(member)
	switch capability {
	case "coordination", "integration", "release":
		return false
	case "implementation":
		return inferred == "implementation" || inferred == "backend" || inferred == "frontend" ||
			inferred == "mobile" || inferred == "infrastructure" || inferred == "documentation"
	case "backend", "frontend", "mobile", "infrastructure", "documentation":
		return inferred == "implementation" || inferred == capability
	case "qa", "review":
		return inferred == capability
	default:
		return false
	}
}

// orchestrationPlanAgentIDs resolves every agent that can receive work from a
// proposed plan before the run exists. This deliberately includes controllers:
// approval-gated steps are parked without agent_id and bind to their controller
// later, so validating only the literal request IDs would leave a private-agent
// and project-squad authorization bypass.
func (h *Handler) orchestrationPlanAgentIDs(
	ctx context.Context,
	workspaceID pgtype.UUID,
	routing orchestrationRouting,
	steps []orchestrationStepRequest,
) ([]pgtype.UUID, error) {
	result := make([]pgtype.UUID, 0, len(steps)+1)
	seen := make(map[pgtype.UUID]struct{}, len(steps)+1)
	add := func(agentID pgtype.UUID) {
		if !agentID.Valid {
			return
		}
		if _, exists := seen[agentID]; exists {
			return
		}
		seen[agentID] = struct{}{}
		result = append(result, agentID)
	}
	add(routing.ControllerAgent)
	for _, input := range steps {
		agentID, err := parseOptionalUUID(input.AgentID)
		if err != nil {
			return nil, fmt.Errorf("invalid step agent_id")
		}
		add(agentID)
		if strings.TrimSpace(input.SquadID) != "" {
			_, controllerID, err := h.resolveSquadStep(ctx, workspaceID, input)
			if err != nil {
				return nil, fmt.Errorf("squad or squad leader not found")
			}
			add(controllerID)
			continue
		}
		if !agentID.Valid && input.ApprovalRequired && !input.HumanOnly {
			controllerID := routing.ControllerAgent
			if !controllerID.Valid {
				controllerID = routing.DevelopmentAgent
			}
			add(controllerID)
		}
	}
	return result, nil
}

// orchestrationFutureAgentIDs returns the effective workforce that a member
// continuation can still cause to run. Completed/cancelled work is historical;
// all other persisted routes and their controllers remain authorization
// relevant because one continuation can unlock the rest of an automatic run.
func orchestrationFutureAgentIDs(steps []db.OrchestrationStep, run db.OrchestrationRun) []pgtype.UUID {
	result := make([]pgtype.UUID, 0, len(steps)+1)
	seen := make(map[pgtype.UUID]struct{}, len(steps)+1)
	add := func(agentID pgtype.UUID) {
		if !agentID.Valid {
			return
		}
		if _, exists := seen[agentID]; exists {
			return
		}
		seen[agentID] = struct{}{}
		result = append(result, agentID)
	}
	hasFutureWork := false
	for _, step := range steps {
		switch step.Status {
		case "completed", "skipped", "cancelled":
			continue
		}
		hasFutureWork = true
		add(step.AgentID)
		add(step.ControllerAgentID)
		if !step.AgentID.Valid && step.ApprovalRequired && !step.ControllerAgentID.Valid {
			add(run.ControllerAgentID)
		}
	}
	if hasFutureWork {
		add(run.ControllerAgentID)
	}
	return result
}

// authorizeOrchestrationAgentIDs applies the same member/private-agent gate as
// chat, mentions, and issue assignment, plus the hard project↔squad workforce
// policy. It is intentionally fail-closed and is used both before creation and
// before every member action that can enqueue or resume orchestration work.
func (h *Handler) authorizeOrchestrationAgentIDs(
	ctx context.Context,
	issue db.Issue,
	actorType, actorID string,
	agentIDs []pgtype.UUID,
) (int, string) {
	seen := make(map[pgtype.UUID]struct{}, len(agentIDs))
	workspaceID := uuidToString(issue.WorkspaceID)
	for _, agentID := range agentIDs {
		if !agentID.Valid {
			continue
		}
		if _, exists := seen[agentID]; exists {
			continue
		}
		seen[agentID] = struct{}{}
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: agentID, WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return http.StatusBadRequest, "orchestration agent not found in workspace"
		}
		if !h.canAccessPrivateAgent(ctx, agent, actorType, actorID, workspaceID) {
			return http.StatusForbidden, "cannot route orchestration work to private agent"
		}
		assigneeType := pgtype.Text{String: "agent", Valid: true}
		if status, message := h.enforceProjectSquadAssignee(ctx, issue.ProjectID, issue.WorkspaceID, assigneeType, agentID); status != 0 {
			return status, message
		}
	}
	return 0, ""
}

func (h *Handler) authorizeOrchestrationRun(
	ctx context.Context,
	issue db.Issue,
	run db.OrchestrationRun,
	actorType, actorID string,
) (int, string) {
	steps, err := h.Queries.ListOrchestrationSteps(ctx, run.ID)
	if err != nil {
		return http.StatusInternalServerError, "load orchestration routes failed"
	}
	return h.authorizeOrchestrationAgentIDs(ctx, issue, actorType, actorID, orchestrationFutureAgentIDs(steps, run))
}

func (h *Handler) authorizeOrchestrationRunRequest(
	w http.ResponseWriter,
	r *http.Request,
	issue db.Issue,
	run db.OrchestrationRun,
	userID string,
) bool {
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	if status, message := h.authorizeOrchestrationRun(r.Context(), issue, run, actorType, actorID); status != 0 {
		writeError(w, status, message)
		return false
	}
	return true
}

func (h *Handler) validateOrchestrationAgentRoute(
	ctx context.Context,
	workspaceID pgtype.UUID,
	runControllerID pgtype.UUID,
	squadID pgtype.UUID,
	stepControllerID pgtype.UUID,
	agentID pgtype.UUID,
	capability string,
) error {
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil {
		return fmt.Errorf("agent not found in workspace")
	}
	ready, reason, err := service.AgentReadiness(ctx, h.Queries, agent)
	if err != nil || !ready {
		if strings.TrimSpace(reason) == "" {
			reason = "agent is not ready"
		}
		return fmt.Errorf("reroute target is not ready: %s", reason)
	}
	if agentID == runControllerID || agentID == stepControllerID {
		return nil
	}
	if !squadID.Valid {
		return fmt.Errorf("reroute target must be the run controller")
	}
	squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: squadID, WorkspaceID: workspaceID})
	if err != nil {
		return fmt.Errorf("step squad not found")
	}
	member := orchestrationPlannerMember{
		AgentID: agent.ID, Name: agent.Name, Description: agent.Description,
		Instructions: agent.Instructions, IsLeader: agent.ID == squad.LeaderID,
		Model: agent.Model.String, ThinkingLevel: agent.ThinkingLevel.String,
		MaxConcurrentTasks: agent.MaxConcurrentTasks,
	}
	if !member.IsLeader {
		members, memberErr := h.Queries.ListSquadMembers(ctx, squadID)
		if memberErr != nil {
			return fmt.Errorf("load step squad members failed")
		}
		found := false
		for _, candidate := range members {
			if candidate.MemberType == "agent" && candidate.MemberID == agentID {
				member.Role = candidate.Role
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("reroute target must be the squad leader or a member")
		}
	}
	if !orchestrationCapabilityCompatible(member, capability) {
		return fmt.Errorf("reroute target is not compatible with %s work", capability)
	}
	return nil
}

const orchestrationArtifactLocationPolicyKey = "artifact_location"

// orchestrationArtifactResourcePolicy mirrors the claim path's effective code
// source: project Git/local resources win, otherwise legacy workspace repos are
// the managed checkout source. Commits in either mode live on one physical
// daemon until Agora has a cross-daemon Git artifact transport.
func (h *Handler) orchestrationArtifactResourcePolicy(ctx context.Context, issue db.Issue) (bool, map[string]bool, error) {
	localDaemons := make(map[string]bool)
	if issue.ProjectID.Valid {
		resources, err := h.Queries.ListProjectResources(ctx, issue.ProjectID)
		if err != nil {
			return false, nil, fmt.Errorf("load project resources: %w", err)
		}
		hasCodeResource := false
		for _, resource := range resources {
			if resource.ResourceType == "github_repo" || resource.ResourceType == "local_directory" {
				hasCodeResource = true
			}
			if resource.ResourceType != "local_directory" {
				continue
			}
			var ref struct {
				DaemonID string `json:"daemon_id"`
			}
			if json.Unmarshal(resource.ResourceRef, &ref) == nil && strings.TrimSpace(ref.DaemonID) != "" {
				localDaemons[strings.TrimSpace(ref.DaemonID)] = true
			}
		}
		if hasCodeResource {
			return true, localDaemons, nil
		}
	}
	workspace, err := h.Queries.GetWorkspace(ctx, issue.WorkspaceID)
	if err != nil {
		return false, nil, fmt.Errorf("load workspace repositories: %w", err)
	}
	var repos []RepoData
	if len(workspace.Repos) > 0 && json.Unmarshal(workspace.Repos, &repos) == nil && len(repos) > 0 {
		return true, localDaemons, nil
	}
	return false, localDaemons, nil
}

func orchestrationArtifactLocationFromPolicy(run db.OrchestrationRun) string {
	var policy map[string]any
	if json.Unmarshal(run.Policy, &policy) != nil {
		return ""
	}
	location, _ := policy[orchestrationArtifactLocationPolicyKey].(string)
	return strings.TrimSpace(location)
}

func (h *Handler) orchestrationRuntimeArtifactLocation(ctx context.Context, runtimeID pgtype.UUID) (string, error) {
	if !runtimeID.Valid {
		return "", fmt.Errorf("artifact stage agent has no runtime")
	}
	runtime, err := h.Queries.GetAgentRuntime(ctx, runtimeID)
	if err != nil {
		return "", fmt.Errorf("artifact stage runtime is unavailable")
	}
	if runtime.DaemonID.Valid && strings.TrimSpace(runtime.DaemonID.String) != "" {
		return "daemon:" + strings.TrimSpace(runtime.DaemonID.String), nil
	}
	return "runtime:" + uuidToString(runtime.ID), nil
}

// validateOrchestrationArtifactRoutes proves that every agent which can touch
// the exact code artifact resolves to one physical daemon (provider-specific
// runtimes on that same daemon are compatible). pinnedLocation is the durable
// run admission result and prevents later reroutes/rebindings from moving the
// artifact pipeline after commits may already exist.
func (h *Handler) validateOrchestrationArtifactRoutes(
	ctx context.Context,
	issue db.Issue,
	agentIDs []pgtype.UUID,
	pinnedLocation string,
) (string, error) {
	required, localDaemons, err := h.orchestrationArtifactResourcePolicy(ctx, issue)
	if err != nil || !required {
		return "", err
	}
	location := ""
	seen := make(map[string]bool, len(agentIDs))
	for _, agentID := range agentIDs {
		key := uuidToString(agentID)
		if !agentID.Valid || seen[key] {
			continue
		}
		seen[key] = true
		agent, agentErr := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: agentID, WorkspaceID: issue.WorkspaceID,
		})
		if agentErr != nil {
			return "", fmt.Errorf("artifact stage agent not found in workspace")
		}
		candidate, runtimeErr := h.orchestrationRuntimeArtifactLocation(ctx, agent.RuntimeID)
		if runtimeErr != nil {
			return "", runtimeErr
		}
		if location == "" {
			location = candidate
		} else if location != candidate {
			return "", fmt.Errorf("implementation, integration, verification, and release agents must run on one daemon so exact Git artifacts remain available to every stage")
		}
	}
	// A human-only plan has no executable agent artifact and therefore no
	// daemon location to validate, even when the project has a local directory.
	if location == "" {
		return "", nil
	}
	if pinnedLocation != "" && location != pinnedLocation {
		return "", fmt.Errorf("artifact stage route moved away from the run's pinned daemon")
	}
	if len(localDaemons) > 0 {
		if !strings.HasPrefix(location, "daemon:") || !localDaemons[strings.TrimPrefix(location, "daemon:")] {
			return "", fmt.Errorf("artifact stage agents must run on a daemon that hosts this project's local directory")
		}
	}
	return location, nil
}

func orchestrationArtifactAgentIDs(steps []db.OrchestrationStep, run db.OrchestrationRun) []pgtype.UUID {
	result := make([]pgtype.UUID, 0, len(steps))
	for _, step := range steps {
		if step.Stage == "plan" || step.Status == "skipped" || step.Status == "cancelled" {
			continue
		}
		agentID := step.AgentID
		if !agentID.Valid && step.ApprovalRequired {
			agentID = step.ControllerAgentID
			if !agentID.Valid {
				agentID = run.ControllerAgentID
			}
		}
		if agentID.Valid {
			result = append(result, agentID)
		}
	}
	return result
}

func (h *Handler) orchestrationPersistedArtifactLocation(ctx context.Context, steps []db.OrchestrationStep) (string, error) {
	location := ""
	for _, step := range steps {
		if step.Stage == "plan" || step.Status == "skipped" || step.Status == "cancelled" || !step.TaskID.Valid {
			continue
		}
		task, err := h.Queries.GetAgentTask(ctx, step.TaskID)
		if err != nil {
			return "", fmt.Errorf("load artifact task runtime: %w", err)
		}
		candidate, err := h.orchestrationRuntimeArtifactLocation(ctx, task.RuntimeID)
		if err != nil {
			return "", err
		}
		if location == "" {
			location = candidate
		} else if location != candidate {
			return "", fmt.Errorf("persisted orchestration artifacts span multiple daemons")
		}
	}
	return location, nil
}

func plannerLeaderWorker(member orchestrationPlannerMember) (orchestrationPlannerMember, bool) {
	worker := member
	worker.IsLeader = false
	if strings.TrimSpace(strings.ToLower(worker.Role)) == "leader" {
		worker.Role = ""
	}
	capability := plannerCapability(worker)
	if capability == "coordination" || capability == "qa" || capability == "review" ||
		!plannerMemberCanPerform(worker, capability) {
		return orchestrationPlannerMember{}, false
	}
	return worker, true
}

func capabilityWork(capability string) (title, instructions string) {
	switch capability {
	case "frontend":
		return "Implement frontend changes", "Own the frontend scope for this issue. Keep the branch focused on user-facing UI and client behavior, document any backend contract assumptions, and leave a committed handoff for integration."
	case "backend":
		return "Implement backend changes", "Own the backend scope for this issue. Keep the branch focused on server, API, and data behavior, document any client contract changes, and leave a committed handoff for integration."
	case "mobile":
		return "Implement mobile changes", "Own the mobile scope for this issue. Keep platform-specific changes isolated, verify the relevant mobile checks, and leave a committed handoff for integration."
	case "infrastructure":
		return "Implement infrastructure changes", "Own the infrastructure and delivery scope for this issue. Keep operational changes isolated, record rollout or migration risks, and leave a committed handoff for integration."
	case "documentation":
		return "Update documentation", "Own the documentation scope for this issue. Keep examples aligned with the implemented contract and leave a committed handoff for integration."
	default:
		return "Implement the core change", "Own the core implementation scope for this issue. Avoid duplicating another worker's specialist scope, verify the changed behavior, and leave a committed handoff for integration."
	}
}

const controllerPlanInstructions = "Analyze the issue, acceptance criteria, repository boundaries, and the persisted capability split. Post one concise PLAN handoff that assigns a non-overlapping outcome to each development branch, records cross-branch contracts and risks, and names the checks integration must run. If a persisted route is unsafe or mismatched, recommend the exact reroute for human acceptance; do not create side tasks, @mention workers, or start later stages."

func plannerMemberByID(members []orchestrationPlannerMember, id pgtype.UUID) (orchestrationPlannerMember, bool) {
	if !id.Valid {
		return orchestrationPlannerMember{}, false
	}
	for _, member := range members {
		if member.AgentID == id {
			return member, true
		}
	}
	return orchestrationPlannerMember{}, false
}

func plannerMemberModel(member orchestrationPlannerMember) string {
	return strings.TrimSpace(member.Model)
}

func plannerMemberThinking(member orchestrationPlannerMember) string {
	return strings.TrimSpace(member.ThinkingLevel)
}

// orchestrationExecutionSnapshot freezes both execution knobs when a step is
// created or rerouted. Valid=true with an empty value is intentional: it pins
// provider-default/no model or thinking instead of falling back to mutable
// agent configuration at claim time.
func orchestrationExecutionSnapshot(
	ctx context.Context,
	queries *db.Queries,
	workspaceID, agentID pgtype.UUID,
	requestedModel string,
) (pgtype.Text, pgtype.Text, error) {
	model := strings.TrimSpace(requestedModel)
	if !agentID.Valid {
		return pgtype.Text{String: model, Valid: model != ""}, pgtype.Text{}, nil
	}
	agent, err := queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID: agentID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return pgtype.Text{}, pgtype.Text{}, err
	}
	if model == "" {
		model = strings.TrimSpace(agent.Model.String)
	}
	return pgtype.Text{String: model, Valid: true}, pgtype.Text{
		String: strings.TrimSpace(agent.ThinkingLevel.String), Valid: true,
	}, nil
}

func plannerMemberConcurrency(member orchestrationPlannerMember) int {
	if member.MaxConcurrentTasks < 1 {
		return 1
	}
	return int(member.MaxConcurrentTasks)
}

// squadRosterPolicyEntry is persisted on the run policy so controllers and
// clients see each squad agent's role and creation-time configuration.
// Generated steps independently persist model and thinking execution pins;
// max concurrency remains a global-capacity snapshot.
type squadRosterPolicyEntry struct {
	AgentID            string `json:"agent_id"`
	Name               string `json:"name"`
	Role               string `json:"role"`
	Capability         string `json:"capability"`
	Model              string `json:"model,omitempty"`
	ThinkingLevel      string `json:"thinking_level,omitempty"`
	MaxConcurrentTasks int    `json:"max_concurrent_tasks"`
	IsLeader           bool   `json:"is_leader,omitempty"`
}

func buildSquadRosterPolicy(members []orchestrationPlannerMember) []squadRosterPolicyEntry {
	roster := make([]squadRosterPolicyEntry, 0, len(members))
	seen := map[string]bool{}
	for _, member := range members {
		agentID := uuidToString(member.AgentID)
		if agentID == "" || seen[agentID] {
			continue
		}
		seen[agentID] = true
		roster = append(roster, squadRosterPolicyEntry{
			AgentID:            agentID,
			Name:               member.Name,
			Role:               strings.TrimSpace(member.Role),
			Capability:         plannerCapability(member),
			Model:              plannerMemberModel(member),
			ThinkingLevel:      plannerMemberThinking(member),
			MaxConcurrentTasks: plannerMemberConcurrency(member),
			IsLeader:           member.IsLeader,
		})
	}
	return roster
}

func controllerPlanInstructionsWithRoster(members []orchestrationPlannerMember, parallelWorkers, maxConcurrency int) string {
	roster := buildSquadRosterPolicy(members)
	if len(roster) == 0 {
		return controllerPlanInstructions
	}
	var b strings.Builder
	b.WriteString(controllerPlanInstructions)
	b.WriteString("\n\nSquad roster snapshot for this run (role + pinned model/think mode + observed global capacity). Assign each branch to the matching specialist; do not treat agents as interchangeable:\n")
	for _, entry := range roster {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = entry.AgentID
		}
		role := entry.Role
		if role == "" {
			role = entry.Capability
		}
		model := entry.Model
		if model == "" {
			model = "agent-default"
		}
		thinking := entry.ThinkingLevel
		if thinking == "" {
			thinking = "none"
		}
		fmt.Fprintf(&b, "- %s [%s / %s]: model=%s thinking=%s max_concurrent_tasks=%d agent_id=%s\n",
			name, role, entry.Capability, model, thinking, entry.MaxConcurrentTasks, entry.AgentID)
	}
	if parallelWorkers < 1 {
		parallelWorkers = 1
	}
	if maxConcurrency < 1 {
		maxConcurrency = parallelWorkers
	}
	fmt.Fprintf(&b, "Parallel development branches in this plan: %d. Run concurrency cap: %d (independent agents may run together; the same agent stays serial within the run).\n",
		parallelWorkers, maxConcurrency)
	return b.String()
}

// squadParallelBudget returns the useful width of the generated graph. Agent
// max_concurrent_tasks is global capacity; Agora deliberately serializes the
// same agent within one issue, so each distinct routed agent contributes at
// most one slot to this run.
func squadParallelBudget(workers []orchestrationPlannerMember, qaAgent, reviewAgent pgtype.UUID, configured int) int {
	if configured < 1 {
		configured = 3
	}
	workerIDs := map[string]struct{}{}
	for _, worker := range workers {
		if id := uuidToString(worker.AgentID); id != "" {
			workerIDs[id] = struct{}{}
		}
	}
	developmentWidth := len(workerIDs)
	if developmentWidth < 1 {
		developmentWidth = 1
	}
	verificationIDs := map[string]struct{}{}
	for _, id := range []pgtype.UUID{qaAgent, reviewAgent} {
		if value := uuidToString(id); value != "" {
			verificationIDs[value] = struct{}{}
		}
	}
	budget := developmentWidth
	if len(verificationIDs) > budget {
		budget = len(verificationIDs)
	}
	if configured < budget {
		return configured
	}
	return budget
}

// squadPlanParallelWidth derives the persisted scheduler cap from the actual
// routes in the plan. Development branches and the QA/review fork are the two
// parallel regions in the generated graph; repeated use of one agent counts
// once because same-issue work is serialized per agent.
func squadPlanParallelWidth(steps []orchestrationStepRequest, configured int) (int, int) {
	developmentAgents := map[string]struct{}{}
	verificationAgents := map[string]struct{}{}
	for _, step := range steps {
		agentID := strings.TrimSpace(step.AgentID)
		if agentID == "" {
			continue
		}
		switch step.Stage {
		case "dev":
			if step.Kind != "integration" {
				developmentAgents[agentID] = struct{}{}
			}
		case "qa", "review":
			verificationAgents[agentID] = struct{}{}
		}
	}
	width := len(developmentAgents)
	if len(verificationAgents) > width {
		width = len(verificationAgents)
	}
	if width < 1 {
		width = 1
	}
	if configured < 1 {
		configured = 3
	}
	if configured < width {
		width = configured
	}
	return width, len(developmentAgents)
}

const (
	squadPlanShapeLean = "lean"
	squadPlanShapeFull = "full"
)

// inferSquadPlanShape keeps the automatic squad path proportional to the
// issue. Most concise, cohesive requests need coordination, one implementer,
// one independent verification pass, and a human release gate—not a branch
// for every roster member. Large or explicitly cross-boundary work keeps the
// full parallel DAG. Humans can force either result with the
// orchestration_shape issue metadata key or provide a custom plan.
func inferSquadPlanShape(issue db.Issue) string {
	if explicit := strings.ToLower(strings.TrimSpace(issueMetadataString(issue.Metadata, "orchestration_shape"))); explicit == squadPlanShapeLean || explicit == squadPlanShapeFull {
		return explicit
	}
	description := ""
	if issue.Description.Valid {
		description = issue.Description.String
	}
	body := strings.ToLower(strings.TrimSpace(issue.Title + "\n" + description))
	// Empty synthetic issues are treated as unknown/full. This is fail-safe and
	// also keeps imported records without useful text from being under-planned.
	if body == "" {
		return squadPlanShapeFull
	}
	for _, marker := range []string{
		"cross-repo", "cross repo", "multiple repositories", "multiple projects",
		"end-to-end", "end to end", "e2e", "data migration", "schema migration",
		"frontend and backend", "backend and frontend", "ui and api", "api and ui",
	} {
		if strings.Contains(body, marker) {
			return squadPlanShapeFull
		}
	}
	if len(body) <= 700 {
		return squadPlanShapeLean
	}
	return squadPlanShapeFull
}

func leanSquadWorker(issue db.Issue, workers []orchestrationPlannerMember) orchestrationPlannerMember {
	if len(workers) == 0 {
		return orchestrationPlannerMember{}
	}
	description := ""
	if issue.Description.Valid {
		description = issue.Description.String
	}
	body := strings.ToLower(issue.Title + "\n" + description)
	capabilityMarkers := map[string][]string{
		"frontend":       {"frontend", "ui", "ux", "screen", "page", "component", "responsive", "css"},
		"backend":        {"backend", "api", "endpoint", "database", "schema", "server"},
		"mobile":         {"mobile", "ios", "android", "swift", "kotlin"},
		"infrastructure": {"infrastructure", "deploy", "docker", "kubernetes", "terraform", "ci/cd"},
		"documentation":  {"documentation", "docs", "readme", "changelog", "markdown"},
	}
	bestIndex, bestScore := 0, -1
	for i, worker := range workers {
		score := 0
		for _, marker := range capabilityMarkers[plannerCapability(worker)] {
			if strings.Contains(body, marker) {
				score++
			}
		}
		if score > bestScore {
			bestIndex, bestScore = i, score
		}
	}
	return workers[bestIndex]
}

func defaultOrchestrationStepsWithMembers(issue db.Issue, routing orchestrationRouting, strategy string, members []orchestrationPlannerMember, configuredConcurrency int) []orchestrationStepRequest {
	return defaultOrchestrationStepsWithMembersAndAutomation(issue, routing, strategy, members, configuredConcurrency, true, true)
}

func defaultOrchestrationStepsWithMembersAndAutomation(issue db.Issue, routing orchestrationRouting, strategy string, members []orchestrationPlannerMember, configuredConcurrency int, autoQA, autoReview bool) []orchestrationStepRequest {
	controller := routing.ControllerAgent
	development := routing.DevelopmentAgent
	if !development.Valid {
		development = controller
	}
	releaseAgent := controller
	if !releaseAgent.Valid {
		releaseAgent = development
	}
	if strategy == "human" {
		return []orchestrationStepRequest{
			{Key: "human-work", Title: "Complete the work", Stage: "dev", Capability: "implementation", ApprovalRequired: true, MaxAttempts: 1},
		}
	}
	if strategy == "solo" {
		// Solo / single-agent assignee: one agent owns the work unit. Runtime-
		// native child agents are intentionally unsupported inside an Agora run;
		// all observable parallel work must be represented as persisted DAG steps.
		return []orchestrationStepRequest{
			{Key: "work", Title: "Implement and verify the change", Stage: "dev", Capability: "implementation", AgentID: uuidToString(development), MaxAttempts: 2},
			{
				// UNROUTED ON PURPOSE — see the release-step note on the squad
				// plan below. The agent is bound at approval time from
				// controller_agent_id, so both attempts stay post-approval.
				Key: "release", Title: "Approve and merge the change", Stage: "release", Capability: "release",
				ApprovalRequired: true, MaxAttempts: 2, DependsOnKeys: []string{"work"},
				Instructions: "After human approval, merge only the exact reviewed change into its configured target branch. Verify the pull request identity and reviewed HEAD before merging; stop and report if either moved.",
			},
		}
	}
	squadID := ""
	if routing.OwnerType == "squad" {
		squadID = uuidToString(routing.OwnerID)
	}
	qaAgent := metadataAgentID(issue, "cast_qa_agent_id")
	reviewAgent := metadataAgentID(issue, "cast_review_agent_id")
	seenAgents := map[string]bool{}
	var workers []orchestrationPlannerMember
	var leaderMember *orchestrationPlannerMember
	for _, member := range members {
		agentID := uuidToString(member.AgentID)
		if agentID == "" || seenAgents[agentID] {
			continue
		}
		seenAgents[agentID] = true
		capability := plannerCapability(member)
		switch capability {
		case "qa":
			if !qaAgent.Valid {
				qaAgent = member.AgentID
			}
		case "review":
			if !reviewAgent.Valid {
				reviewAgent = member.AgentID
			}
		case "coordination":
			leader := member
			leaderMember = &leader
		default:
			if plannerMemberCanPerform(member, capability) {
				workers = append(workers, member)
			}
		}
	}
	// A developer lead may own one implementation branch after planning and
	// before integration. Use that capacity only when the ready roster has
	// fewer than two compatible workers, preserving specialist-only plans that
	// already have real parallel coverage.
	if len(workers) < 2 && leaderMember != nil {
		if leaderWorker, ok := plannerLeaderWorker(*leaderMember); ok {
			workers = append(workers, leaderWorker)
		}
	}
	if !qaAgent.Valid {
		qaAgent = controller
	}
	if !reviewAgent.Valid {
		reviewAgent = controller
	}
	if len(workers) == 0 {
		workers = []orchestrationPlannerMember{{AgentID: development, Role: "implementation"}}
	}
	planShape := inferSquadPlanShape(issue)
	if planShape == squadPlanShapeLean && autoQA && autoReview {
		workers = []orchestrationPlannerMember{leanSquadWorker(issue, workers)}
	}

	controllerModel := ""
	if member, ok := plannerMemberByID(members, controller); ok {
		controllerModel = plannerMemberModel(member)
	}
	qaModel := ""
	if member, ok := plannerMemberByID(members, qaAgent); ok {
		qaModel = plannerMemberModel(member)
	}
	reviewModel := ""
	if member, ok := plannerMemberByID(members, reviewAgent); ok {
		reviewModel = plannerMemberModel(member)
	}
	maxConcurrency := squadParallelBudget(workers, qaAgent, reviewAgent, configuredConcurrency)

	steps := []orchestrationStepRequest{{
		Key: "plan", Title: "Plan the work", Stage: "plan", Capability: "coordination", AgentID: uuidToString(controller),
		Model: controllerModel, MaxAttempts: 2, SquadID: squadID,
		Instructions: controllerPlanInstructionsWithRoster(members, len(workers), maxConcurrency),
	}}
	capabilityCounts := map[string]int{}
	developmentKeys := make([]string, 0, len(workers))
	for _, worker := range workers {
		capability := plannerCapability(worker)
		capabilityCounts[capability]++
		key := "dev-" + capability
		if len(workers) == 1 && capability == "implementation" {
			key = "dev"
		} else if capabilityCounts[capability] > 1 {
			key = fmt.Sprintf("%s-%d", key, capabilityCounts[capability])
		}
		title, instructions := capabilityWork(capability)
		steps = append(steps, orchestrationStepRequest{
			Key: key, Title: title, Stage: "dev", Capability: capability, AgentID: uuidToString(worker.AgentID),
			Model: plannerMemberModel(worker), Instructions: instructions, MaxAttempts: 2,
			DependsOnKeys: []string{"plan"}, ParentKey: "plan", SquadID: squadID,
		})
		developmentKeys = append(developmentKeys, key)
	}
	integrationAgent := controller
	if !integrationAgent.Valid {
		integrationAgent = development
	}
	verificationDependencies := append([]string(nil), developmentKeys...)
	if len(developmentKeys) > 1 {
		steps = append(steps, orchestrationStepRequest{
			Key: "integrate", Title: "Integrate implementation branches", Stage: "dev", Kind: "integration", Capability: "integration",
			AgentID: uuidToString(integrationAgent), Model: controllerModel, MaxAttempts: 2, DependsOnKeys: developmentKeys, SquadID: squadID,
		})
		verificationDependencies = []string{"integrate"}
	}
	if planShape == squadPlanShapeLean {
		steps = append(steps,
			orchestrationStepRequest{
				Key: "verify", Title: "Verify and review the result", Stage: "qa", Capability: "qa",
				AgentID: uuidToString(qaAgent), Model: qaModel, MaxAttempts: 2,
				DependsOnKeys: verificationDependencies, SquadID: squadID,
				Instructions: "Independently test the completed outcome and review the changed code and contracts. Report one combined verdict with concrete evidence; do not edit the reviewed artifact.",
			},
			orchestrationStepRequest{
				Key: "release", Title: "Approve and merge the change", Stage: "release", Capability: "release",
				ApprovalRequired: true, MaxAttempts: 2, DependsOnKeys: []string{"verify"}, SquadID: squadID,
				Instructions: "After human approval, merge only the exact artifact verified by the prior step into its configured target branch. Verify the pull request identity and reviewed HEAD before merging; stop and report if either moved.",
			},
		)
		return steps
	}
	releaseDependencies := append([]string(nil), verificationDependencies...)
	if autoQA {
		steps = append(steps, orchestrationStepRequest{
			Key: "qa", Title: "Verify the completed result", Stage: "qa", Capability: "qa",
			AgentID: uuidToString(qaAgent), Model: qaModel, MaxAttempts: 2,
			DependsOnKeys: verificationDependencies, SquadID: squadID,
		})
		releaseDependencies = []string{"qa"}
	}
	if !autoQA && autoReview {
		steps = append(steps, orchestrationStepRequest{
			Key: "manual-qa", Title: "Manual QA", Stage: "qa", Capability: "qa",
			ApprovalRequired: true, HumanOnly: true, MaxAttempts: 1,
			DependsOnKeys: releaseDependencies,
			Instructions:  "Run the project's manual QA checks against the completed build and approve this checkpoint only when the evidence is acceptable.",
		})
		releaseDependencies = []string{"manual-qa"}
	}
	if autoReview {
		steps = append(steps, orchestrationStepRequest{
			Key: "review", Title: "Review the completed result", Stage: "review", Capability: "review",
			AgentID: uuidToString(reviewAgent), Model: reviewModel, MaxAttempts: 2,
			DependsOnKeys: releaseDependencies, SquadID: squadID,
		})
		releaseDependencies = []string{"review"}
	} else {
		title := "Manual review"
		instructions := "Review the completed artifact and record the human decision before continuing."
		if !autoQA {
			title = "Manual QA and review"
			instructions = "Run the project's manual QA checks against the completed build, review the result, and approve this checkpoint only when the evidence is acceptable."
		}
		steps = append(steps, orchestrationStepRequest{
			Key: "manual-review", Title: title, Stage: "review", Capability: "review",
			ApprovalRequired: true, HumanOnly: true, MaxAttempts: 1,
			DependsOnKeys: releaseDependencies, Instructions: instructions,
		})
		releaseDependencies = []string{"manual-review"}
	}
	steps = append(steps,
		orchestrationStepRequest{
			// UNROUTED ON PURPOSE. A release step that carries an agent_id is
			// dispatched by the scheduler like any other step — BEFORE the human
			// approves — because the park rule is `ApprovalRequired && !AgentID`.
			// That burned a whole agent turn on work nobody had authorized yet,
			// and with MaxAttempts 1 it also consumed the only attempt, so the
			// post-approval QueueOrchestrationStep failed and the merge never
			// ran at all. Leaving agent_id NULL parks the step until approval;
			// ApproveOrchestrationStep then binds the agent from
			// controller_agent_id (COALESCE) and dispatches it. MaxAttempts 2
			// because both attempts are now post-approval and a merge is worth
			// one retry.
			Key: "release", Title: "Approve and merge the change", Stage: "release", Capability: "release",
			ApprovalRequired: true, MaxAttempts: 2, DependsOnKeys: releaseDependencies, SquadID: squadID,
			Instructions: "After human approval, merge only the exact integrated artifact verified by QA and review into its configured target branch. Verify the pull request identity and reviewed HEAD before merging; stop and report if either moved.",
		},
	)
	return steps
}

func (h *Handler) orchestrationPlannerMembers(ctx context.Context, issue db.Issue, routing orchestrationRouting, strategy string) []orchestrationPlannerMember {
	if strategy != "squad" || routing.OwnerType != "squad" || !routing.OwnerID.Valid {
		return nil
	}
	members, err := h.Queries.ListSquadMembers(ctx, routing.OwnerID)
	if err != nil {
		return nil
	}
	result := make([]orchestrationPlannerMember, 0, len(members))
	for _, member := range members {
		if member.MemberType != "agent" {
			continue
		}
		agent, agentErr := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: member.MemberID, WorkspaceID: issue.WorkspaceID})
		if agentErr != nil {
			continue
		}
		ready, _, readinessErr := service.AgentReadiness(ctx, h.Queries, agent)
		if readinessErr != nil || !ready {
			continue
		}
		result = append(result, orchestrationPlannerMember{
			AgentID: agent.ID, Name: agent.Name, Role: member.Role, Description: agent.Description,
			Instructions: agent.Instructions, IsLeader: agent.ID == routing.ControllerAgent,
			Model: agent.Model.String, ThinkingLevel: agent.ThinkingLevel.String,
			MaxConcurrentTasks: agent.MaxConcurrentTasks,
		})
	}
	return result
}

// CreateIssueOrchestration creates an immutable ordered plan. Custom plans can
// route every step to a different agent/model; an empty steps array produces
// the standard plan→dev→QA→review→human-release chain.
func (h *Handler) CreateIssueOrchestration(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	var req orchestrationPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	projectDefaults := h.orchestrationDefaultsForIssue(r.Context(), issue)
	if strings.TrimSpace(req.ProgressionPolicy) != "" && normalizeProgressionPolicy(req.ProgressionPolicy) == "" {
		writeError(w, http.StatusBadRequest, "progression_policy must be automatic, gated, or manual")
		return
	}
	if strings.TrimSpace(req.Mode) != "" && !strings.EqualFold(req.Mode, "auto") && !strings.EqualFold(req.Mode, "manual") {
		writeError(w, http.StatusBadRequest, "mode must be auto or manual")
		return
	}
	progressionPolicy := progressionPolicyForIssue(issue, req.ProgressionPolicy, req.Mode, projectDefaults.ProgressionPolicy)
	if progressionPolicy == "" {
		writeError(w, http.StatusBadRequest, "progression_policy must be automatic, gated, or manual")
		return
	}
	routing := h.orchestrationRouting(r.Context(), issue)
	hasCustomPlan := len(req.Steps) > 0
	executionStrategy := strings.ToLower(strings.TrimSpace(req.ExecutionStrategy))
	if executionStrategy == "" {
		executionStrategy = resolveExecutionStrategy(routing, projectDefaults.ExecutionStrategy, hasCustomPlan)
	}
	if !validExecutionStrategy(executionStrategy) {
		writeError(w, http.StatusBadRequest, "execution_strategy must be human, solo, squad, or custom")
		return
	}
	if executionStrategy == "custom" && !hasCustomPlan {
		writeError(w, http.StatusBadRequest, "custom execution_strategy requires explicit steps")
		return
	}
	requestedSquadID := strings.TrimSpace(req.SquadID)
	if requestedSquadID == "" && executionStrategy == "squad" && routing.OwnerType != "squad" {
		requestedSquadID = projectDefaults.SquadID
	}
	if requestedSquadID != "" {
		if executionStrategy != "squad" {
			writeError(w, http.StatusBadRequest, "squad_id is only valid for squad execution")
			return
		}
		squadID, parseErr := parseUUIDValue(requestedSquadID)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid squad_id")
			return
		}
		assigneeType := pgtype.Text{String: "squad", Valid: true}
		if status, message := h.validateAssigneePair(r.Context(), r, uuidToString(issue.WorkspaceID), assigneeType, squadID); status != 0 {
			writeError(w, status, message)
			return
		}
		if status, message := h.enforceProjectSquadAssignee(r.Context(), issue.ProjectID, issue.WorkspaceID, assigneeType, squadID); status != 0 {
			writeError(w, status, message)
			return
		}
		squad, squadErr := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
			ID: squadID, WorkspaceID: issue.WorkspaceID,
		})
		if squadErr != nil || !squad.LeaderID.Valid {
			writeError(w, http.StatusBadRequest, "squad or squad leader not found")
			return
		}
		// Run ownership is independent from issue assignment. An explicit Squad
		// selection must route planning/integration through that squad's leader
		// and load that squad's capability roster even when the issue is still
		// assigned to an individual agent.
		routing = orchestrationRouting{
			OwnerType:        "squad",
			OwnerID:          squad.ID,
			ControllerAgent:  squad.LeaderID,
			DevelopmentAgent: squad.LeaderID,
			ExecutionMode:    "squad",
		}
	}
	if executionStrategy == "solo" && routing.OwnerType == "agent" {
		routing = applySoloAgentRouting(routing)
	}
	if executionStrategy == "solo" && !routing.DevelopmentAgent.Valid {
		writeError(w, http.StatusBadRequest, "solo execution requires an agent-assigned issue")
		return
	}
	if executionStrategy == "squad" && routing.OwnerType != "squad" {
		writeError(w, http.StatusBadRequest, "squad_id is required when the issue is not assigned to a squad")
		return
	}
	plannerMembers := []orchestrationPlannerMember(nil)
	if executionStrategy == "squad" {
		leader, leaderErr := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID: routing.ControllerAgent, WorkspaceID: issue.WorkspaceID,
		})
		if leaderErr != nil {
			writeError(w, http.StatusBadRequest, "squad leader not found in workspace")
			return
		}
		ready, reason, readinessErr := service.AgentReadiness(r.Context(), h.Queries, leader)
		if readinessErr != nil {
			writeError(w, http.StatusInternalServerError, "check squad leader readiness failed")
			return
		}
		if !ready {
			writeError(w, http.StatusConflict, "squad leader is not ready: "+reason)
			return
		}
		plannerMembers = h.orchestrationPlannerMembers(r.Context(), issue, routing, executionStrategy)
	}
	configuredConcurrency := 3
	if projectDefaults.MaxConcurrency > 0 {
		configuredConcurrency = projectDefaults.MaxConcurrency
	}
	if req.Policy != nil {
		if configured, ok := req.Policy["max_concurrency"].(float64); ok && configured >= 1 && configured <= 10 {
			configuredConcurrency = int(configured)
		}
	}
	autoQA := h.autoQAEnabled(r.Context(), issue)
	autoReview := h.autoReviewEnabled(r.Context(), issue)
	if !hasCustomPlan {
		req.Steps = defaultOrchestrationStepsWithMembersAndAutomation(
			issue, routing, executionStrategy, plannerMembers, configuredConcurrency,
			autoQA, autoReview,
		)
	}
	if len(req.Steps) > 20 {
		writeError(w, http.StatusBadRequest, "plan cannot exceed 20 steps")
		return
	}
	if err := prepareOrchestrationPlan(req.Steps); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	effectiveAgentIDs, routeErr := h.orchestrationPlanAgentIDs(r.Context(), issue.WorkspaceID, routing, req.Steps)
	if routeErr != nil {
		writeError(w, http.StatusBadRequest, routeErr.Error())
		return
	}
	if status, message := h.authorizeOrchestrationAgentIDs(
		r.Context(), issue, actorType, actorID, effectiveAgentIDs,
	); status != 0 {
		writeError(w, status, message)
		return
	}
	// Validate every explicit executable route before creating the run or
	// touching a runtime queue. A syntactically valid UUID is not authority to
	// route work to an agent owned by another workspace.
	for _, input := range req.Steps {
		agentID, parseErr := parseOptionalUUID(input.AgentID)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid step agent_id")
			return
		}
		if !agentID.Valid {
			continue
		}
		agent, agentErr := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID: agentID, WorkspaceID: issue.WorkspaceID,
		})
		if agentErr != nil {
			writeError(w, http.StatusBadRequest, "step agent not found in workspace")
			return
		}
		ready, reason, readinessErr := service.AgentReadiness(r.Context(), h.Queries, agent)
		if readinessErr != nil {
			writeError(w, http.StatusInternalServerError, "check step agent readiness failed")
			return
		}
		if !ready {
			writeError(w, http.StatusConflict, "step agent is not ready: "+reason)
			return
		}
	}
	// Git commits produced by a daemon are currently durable in that runtime's
	// managed repository cache (or its bound local_directory source), not in a
	// cross-runtime artifact store. Letting implementation run on one daemon and
	// integration/QA on another would create a valid-looking SHA that the next
	// stage cannot fetch. Reject that topology before a run exists instead of
	// failing late after workers have spent their turns. Multiple agents still
	// run independently and in parallel when they share the same runtime.
	artifactAgentIDs := make([]pgtype.UUID, 0, len(req.Steps))
	for _, input := range req.Steps {
		if input.Stage == "plan" {
			continue
		}
		agentID, _ := parseOptionalUUID(input.AgentID)
		if !agentID.Valid && input.ApprovalRequired && !input.HumanOnly {
			if strings.TrimSpace(input.SquadID) != "" {
				_, controllerID, resolveErr := h.resolveSquadStep(r.Context(), issue.WorkspaceID, input)
				if resolveErr != nil {
					writeError(w, http.StatusBadRequest, "squad or squad leader not found")
					return
				}
				agentID = controllerID
			} else {
				agentID = routing.ControllerAgent
				if !agentID.Valid {
					agentID = routing.DevelopmentAgent
				}
			}
		}
		if !agentID.Valid {
			continue
		}
		artifactAgentIDs = append(artifactAgentIDs, agentID)
	}
	artifactLocation, artifactErr := h.validateOrchestrationArtifactRoutes(r.Context(), issue, artifactAgentIDs, "")
	if artifactErr != nil {
		writeError(w, http.StatusConflict, artifactErr.Error())
		return
	}
	if req.Policy == nil {
		req.Policy = make(map[string]any)
	}
	req.Policy["owner_type"] = routing.OwnerType
	req.Policy["owner_id"] = uuidToString(routing.OwnerID)
	req.Policy["controller_agent_id"] = uuidToString(routing.ControllerAgent)
	req.Policy["execution_strategy"] = executionStrategy
	req.Policy["progression_policy"] = progressionPolicy
	req.Policy["auto_qa"] = autoQA
	req.Policy["auto_review"] = autoReview
	if !autoQA && !autoReview {
		req.Policy["verification_mode"] = "manual"
	} else {
		req.Policy["verification_mode"] = "agent"
	}
	if artifactLocation != "" {
		req.Policy[orchestrationArtifactLocationPolicyKey] = artifactLocation
	}
	if executionStrategy == "squad" && routing.OwnerType == "squad" {
		req.Policy["squad_id"] = uuidToString(routing.OwnerID)
		if hasCustomPlan {
			req.Policy["plan_shape"] = "custom"
		} else {
			req.Policy["plan_shape"] = inferSquadPlanShape(issue)
		}
		if roster := buildSquadRosterPolicy(plannerMembers); len(roster) > 0 {
			req.Policy["squad_roster"] = roster
		}
	}
	// Compatibility field for old clients; never read as the new source of
	// truth after the run row is created.
	req.Policy["execution_mode"] = legacyExecutionMode(executionStrategy)
	if executionStrategy == "squad" {
		// Always overwrite a client-supplied cap with the actual plan width. The
		// controller prompt and dispatcher must enforce the same value.
		width, parallelWorkers := squadPlanParallelWidth(req.Steps, configuredConcurrency)
		req.Policy["max_concurrency"] = width
		req.Policy["parallel_workers"] = parallelWorkers
	} else if _, configured := req.Policy["max_concurrency"]; !configured {
		req.Policy["max_concurrency"] = configuredConcurrency
	}
	autoStart := true
	if req.AutoStart != nil {
		autoStart = *req.AutoStart
	} else if projectDefaults.ReviewPlanFirst != nil {
		autoStart = !*projectDefaults.ReviewPlanFirst
	}
	req.Policy[manualDispatchAuthorizationPolicyKey] = progressionPolicy != "manual" || autoStart
	policy, _ := json.Marshal(req.Policy)
	takenOverTasks := 0
	if autoStart {
		activeTasks, activeErr := h.Queries.ListActiveTasksByIssue(r.Context(), issue.ID)
		if activeErr != nil {
			writeError(w, http.StatusInternalServerError, "check active issue tasks failed")
			return
		}
		// Assignment to an agent/squad may enqueue the normal on-assignment task
		// milliseconds before the user starts orchestration. Starting the run is
		// an explicit transfer of control: cancel those ordinary tasks first so
		// the controller is the issue's sole dispatcher.
		for _, task := range activeTasks {
			if task.OrchestrationStepID.Valid {
				writeError(w, http.StatusConflict, "an orchestration task is already active")
				return
			}
		}
		if len(activeTasks) > 0 {
			if cancelErr := h.TaskService.CancelTasksForIssue(r.Context(), issue.ID); cancelErr != nil {
				writeError(w, http.StatusConflict, "could not take control of active issue tasks")
				return
			}
			takenOverTasks = len(activeTasks)
		}
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusInternalServerError, "orchestration transactions are not configured")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin orchestration plan failed")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	run, err := qtx.CreateOrchestrationRun(r.Context(), db.CreateOrchestrationRunParams{
		WorkspaceID:       issue.WorkspaceID,
		IssueID:           issue.ID,
		Mode:              legacyOrchestrationMode(progressionPolicy),
		Policy:            policy,
		CreatedBy:         parseUUID(userID),
		ExecutionStrategy: executionStrategy,
		ProgressionPolicy: progressionPolicy,
		OwnerType:         routing.OwnerType,
		OwnerID:           routing.OwnerID,
		ControllerAgentID: routing.ControllerAgent,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "this issue already has an active orchestration")
		return
	}
	stepsByKey := make(map[string]db.OrchestrationStep, len(req.Steps))
	for index, input := range req.Steps {
		input.Key = strings.TrimSpace(input.Key)
		input.Title = strings.TrimSpace(input.Title)
		if input.Kind == "" {
			input.Kind = "task"
		}
		if input.Key == "" || input.Title == "" || !validOrchestrationStage(input.Stage) {
			writeError(w, http.StatusBadRequest, "every step requires a unique key, title, and valid stage")
			return
		}
		if input.Kind != "task" && input.Kind != "integration" {
			writeError(w, http.StatusBadRequest, "step kind must be task or integration")
			return
		}
		agentID, parseErr := parseOptionalUUID(input.AgentID)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid step agent_id")
			return
		}
		var squadID, controllerAgentID pgtype.UUID
		if strings.TrimSpace(input.SquadID) != "" {
			squadID, controllerAgentID, parseErr = h.resolveSquadStep(r.Context(), issue.WorkspaceID, input)
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "squad or squad leader not found")
				return
			}
			// An approval-required step is left UNROUTED on purpose: the
			// scheduler parks a step only while `ApprovalRequired && !AgentID`,
			// so filling the agent here would dispatch it before the human
			// approved. controller_agent_id below still records WHO will run it;
			// ApproveOrchestrationStep binds it at approval time.
			if !agentID.Valid && !input.ApprovalRequired {
				agentID = controllerAgentID
			}
			if agentID.Valid && agentID != controllerAgentID {
				isMember, memberErr := h.Queries.IsSquadMember(r.Context(), db.IsSquadMemberParams{SquadID: squadID, MemberType: "agent", MemberID: agentID})
				if memberErr != nil || !isMember {
					writeError(w, http.StatusBadRequest, "squad step agent must be the leader or a member of that squad")
					return
				}
			}
		}
		// A parked approval step needs a controller to bind to when the human
		// approves. The squad branch above supplies one; a SOLO plan has no
		// squad, so without this fallback controller_agent_id stays NULL,
		// ApproveOrchestrationStep's COALESCE yields NULL, and the step is
		// marked `completed` with no merge ever dispatched — the approval
		// silently does nothing.
		if input.HumanOnly && (!input.ApprovalRequired || agentID.Valid) {
			writeError(w, http.StatusBadRequest, "human-only steps must be unrouted approval gates")
			return
		}
		if input.ApprovalRequired && !input.HumanOnly && !agentID.Valid && !controllerAgentID.Valid {
			controllerAgentID = routing.ControllerAgent
			if !controllerAgentID.Valid {
				controllerAgentID = routing.DevelopmentAgent
			}
		}
		if !agentID.Valid && !input.ApprovalRequired {
			writeError(w, http.StatusBadRequest, "executable steps require an agent_id")
			return
		}
		if input.MaxAttempts < 1 {
			input.MaxAttempts = 2
		}
		var legacyDependency pgtype.UUID
		for _, key := range input.DependsOnKeys {
			dependency, exists := stepsByKey[strings.TrimSpace(key)]
			if !exists {
				writeError(w, http.StatusBadRequest, "step dependencies must reference earlier step keys")
				return
			}
			if !legacyDependency.Valid {
				legacyDependency = dependency.ID
			}
		}
		var parentStepID pgtype.UUID
		if strings.TrimSpace(input.ParentKey) != "" {
			parent, exists := stepsByKey[strings.TrimSpace(input.ParentKey)]
			if !exists {
				writeError(w, http.StatusBadRequest, "parent_key must reference an earlier step")
				return
			}
			parentStepID = parent.ID
		}
		snapshotAgentID := agentID
		if !snapshotAgentID.Valid {
			snapshotAgentID = controllerAgentID
		}
		modelOverride, thinkingLevelOverride, snapshotErr := orchestrationExecutionSnapshot(
			r.Context(), qtx, issue.WorkspaceID, snapshotAgentID, input.Model,
		)
		if snapshotErr != nil {
			writeError(w, http.StatusBadRequest, "step agent not found in workspace")
			return
		}
		step, createErr := qtx.CreateOrchestrationStep(r.Context(), db.CreateOrchestrationStepParams{
			RunID: run.ID, StepKey: input.Key, Title: input.Title, Stage: input.Stage,
			Position: int32(index), AgentID: agentID,
			ModelOverride: modelOverride, ThinkingLevelOverride: thinkingLevelOverride,
			DependsOnStepID: legacyDependency, ApprovalRequired: input.ApprovalRequired,
			MaxAttempts: input.MaxAttempts, Instructions: strings.TrimSpace(input.Instructions),
			ParentStepID: parentStepID, SquadID: squadID, ControllerAgentID: controllerAgentID,
			IntroducedInVersion: 1, StepKind: input.Kind, Capability: input.Capability,
		})
		if createErr != nil {
			writeError(w, http.StatusBadRequest, "step keys must be unique")
			return
		}
		stepsByKey[input.Key] = step
		for _, key := range input.DependsOnKeys {
			dependency := stepsByKey[strings.TrimSpace(key)]
			if depErr := qtx.AddOrchestrationStepDependency(r.Context(), db.AddOrchestrationStepDependencyParams{StepID: step.ID, DependsOnStepID: dependency.ID}); depErr != nil {
				writeError(w, http.StatusBadRequest, "invalid step dependency")
				return
			}
		}
	}
	planEvent := "plan_proposed"
	if autoStart {
		planEvent = "plan_created"
	}
	eventDetails, _ := json.Marshal(map[string]any{"steps": len(req.Steps), "taken_over_tasks": takenOverTasks})
	planCreatedEvent, err := qtx.CreateOrchestrationEvent(r.Context(), db.CreateOrchestrationEventParams{
		RunID: run.ID, Kind: planEvent, ActorType: "member", ActorID: parseUUID(userID), Details: eventDetails,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create orchestration plan event failed")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "commit orchestration plan failed")
		return
	}
	h.publishOrchestrationChanged(r.Context(), run, planCreatedEvent)
	if autoStart {
		if err := h.dispatchNextOrchestrationStep(r.Context(), run.ID, issue); err != nil {
			slog.Warn("start orchestration failed", "run_id", uuidToString(run.ID), "error", err)
		}
	}
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusCreated)
}

func (h *Handler) GetIssueOrchestration(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusOK)
}

func (h *Handler) writeIssueOrchestration(w http.ResponseWriter, r *http.Request, issueID pgtype.UUID, status int) {
	run, err := h.Queries.GetLatestOrchestrationRunForIssue(r.Context(), issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, status, nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load orchestration failed")
		return
	}
	steps, _ := h.Queries.ListOrchestrationSteps(r.Context(), run.ID)
	dependencies, _ := h.Queries.ListOrchestrationStepDependencies(r.Context(), run.ID)
	events, _ := h.Queries.ListOrchestrationEvents(r.Context(), run.ID)
	messages, _ := h.Queries.ListOrchestrationMessages(r.Context(), run.ID)
	revisions, _ := h.Queries.ListOrchestrationPlanRevisions(r.Context(), run.ID)
	response := orchestrationRunResponse{
		ID: uuidToString(run.ID), IssueID: uuidToString(run.IssueID), Status: run.Status, Mode: run.Mode,
		ExecutionStrategy: run.ExecutionStrategy, ProgressionPolicy: run.ProgressionPolicy,
		Policy: json.RawMessage(run.Policy), CreatedAt: run.CreatedAt.Time, UpdatedAt: run.UpdatedAt.Time,
		PlanVersion: run.PlanVersion, Steps: make([]orchestrationStepResponse, 0, len(steps)), Events: make([]orchestrationEventResponse, 0, len(events)), Messages: make([]orchestrationMessageResponse, 0, len(messages)), Revisions: make([]orchestrationRevisionResponse, 0, len(revisions)),
		OwnerType: run.OwnerType, OwnerID: uuidToString(run.OwnerID), ControllerAgentID: uuidToString(run.ControllerAgentID),
		BaseGitStates: json.RawMessage(run.BaseGitStates),
		ExecutionMode: legacyExecutionMode(run.ExecutionStrategy),
	}
	if run.StartedAt.Valid {
		response.StartedAt = run.StartedAt.Time
	}
	if run.CompletedAt.Valid {
		response.CompletedAt = run.CompletedAt.Time
	}
	openQuestionIDs := make(map[pgtype.UUID]string)
	for _, message := range messages {
		if message.Kind == "question" && message.ExpectsReply && !message.ResolvedAt.Valid {
			openQuestionIDs[message.StepID] = uuidToString(message.ID)
		}
	}
	for _, step := range steps {
		item := orchestrationStepResponse{
			ID: uuidToString(step.ID), Key: step.StepKey, Title: step.Title, Stage: step.Stage,
			Status: step.Status, Position: step.Position, AgentID: uuidToString(step.AgentID), Model: step.ModelOverride.String,
			TaskID: uuidToString(step.TaskID), QuestionID: openQuestionIDs[step.ID], ApprovalRequired: step.ApprovalRequired,
			ApprovedBy: uuidToString(step.ApprovedBy), Attempt: step.Attempt, MaxAttempts: step.MaxAttempts,
			Instructions: step.Instructions, Error: step.Error.String, DependsOnStepIDs: []string{},
			ParentStepID: uuidToString(step.ParentStepID), SquadID: uuidToString(step.SquadID), ControllerAgentID: uuidToString(step.ControllerAgentID),
			WorktreeBranch: step.WorktreeBranch.String, BaseSHA: step.BaseSha.String, HeadSHA: step.HeadSha.String, MergeStatus: step.MergeStatus, ConflictFiles: step.ConflictFiles,
			Kind: step.StepKind, Capability: step.Capability, IntegrationStatus: step.IntegrationStatus, IntegratedHeadSHAs: step.IntegratedHeadShas, MissingHeadSHAs: step.MissingHeadShas,
		}
		if step.ThinkingLevelOverride.Valid {
			thinkingLevel := step.ThinkingLevelOverride.String
			item.ThinkingLevel = &thinkingLevel
		}
		for _, dependency := range dependencies {
			if dependency.StepID == step.ID {
				item.DependsOnStepIDs = append(item.DependsOnStepIDs, uuidToString(dependency.DependsOnStepID))
			}
		}
		if len(step.Output) > 0 {
			item.Output = json.RawMessage(step.Output)
		}
		response.Steps = append(response.Steps, item)
	}
	for _, event := range events {
		response.Events = append(response.Events, orchestrationEventResponse{
			ID: uuidToString(event.ID), StepID: uuidToString(event.StepID), Kind: event.Kind,
			ActorType: event.ActorType, ActorID: uuidToString(event.ActorID), Details: json.RawMessage(event.Details), CreatedAt: event.CreatedAt.Time,
		})
	}
	for _, message := range messages {
		item := orchestrationMessageResponse{
			ID: uuidToString(message.ID), StepID: uuidToString(message.StepID), Kind: message.Kind,
			ActorType: message.ActorType, ActorID: uuidToString(message.ActorID), TargetType: message.TargetType,
			TargetID: uuidToString(message.TargetID), Body: json.RawMessage(message.Body), PlanVersion: message.PlanVersion,
			CorrelationID: uuidToString(message.CorrelationID), CausationID: uuidToString(message.CausationID),
			ReplyToID: uuidToString(message.ReplyToID), ExpectsReply: message.ExpectsReply, CreatedAt: message.CreatedAt.Time,
		}
		if message.AcknowledgedAt.Valid {
			item.AcknowledgedAt = message.AcknowledgedAt.Time
		}
		if message.ResolvedAt.Valid {
			item.ResolvedAt = message.ResolvedAt.Time
		}
		response.Messages = append(response.Messages, item)
	}
	for _, revision := range revisions {
		response.Revisions = append(response.Revisions, orchestrationRevisionResponse{ID: uuidToString(revision.ID), Version: revision.Version, ActorType: revision.ActorType, ActorID: uuidToString(revision.ActorID), Reason: revision.Reason, Patch: revision.Patch, CreatedAt: revision.CreatedAt.Time})
	}
	writeJSON(w, status, response)
}

func (h *Handler) EditIssueOrchestration(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	var req orchestrationPlanEditRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedVersion < 1 || strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "expected_version and reason are required")
		return
	}
	run, err := h.Queries.GetActiveOrchestrationRunForIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no active orchestration")
		return
	}
	stepID, ok := parseUUIDOrBadRequest(w, req.StepID, "step_id")
	if !ok {
		return
	}
	step, err := h.Queries.GetOrchestrationStep(r.Context(), stepID)
	if err != nil || step.RunID != run.ID {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	var agentID pgtype.UUID
	var rerouteModel, rerouteThinkingLevel pgtype.Text
	var extraDependencies []pgtype.UUID
	var integrationJoinID pgtype.UUID
	var insertPosition int32
	if req.Operation == "reroute" {
		agentID, err = parseUUIDValue(req.AgentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "valid agent_id is required")
			return
		}
		if status, message := h.authorizeOrchestrationAgentIDs(
			r.Context(), issue, actorType, actorID, []pgtype.UUID{agentID},
		); status != 0 {
			writeError(w, status, message)
			return
		}
		if routeErr := h.validateOrchestrationAgentRoute(
			r.Context(), issue.WorkspaceID, run.ControllerAgentID, step.SquadID,
			step.ControllerAgentID, agentID, step.Capability,
		); routeErr != nil {
			writeError(w, http.StatusBadRequest, routeErr.Error())
			return
		}
		rerouteModel, rerouteThinkingLevel, err = orchestrationExecutionSnapshot(
			r.Context(), h.Queries, issue.WorkspaceID, agentID, req.Model,
		)
		if err != nil {
			writeError(w, http.StatusBadRequest, "step agent not found in workspace")
			return
		}
	} else if req.Operation == "add_child" {
		if run.Status != "draft" {
			writeError(w, http.StatusConflict, "structural plan edits are only allowed on a draft proposal")
			return
		}
		if req.Child == nil || strings.TrimSpace(req.Child.Key) == "" || strings.TrimSpace(req.Child.Title) == "" || !validOrchestrationStage(req.Child.Stage) {
			writeError(w, http.StatusBadRequest, "child key, title, and valid stage are required")
			return
		}
		if !step.SquadID.Valid {
			writeError(w, http.StatusConflict, "children can only be delegated from a squad step")
			return
		}
		if (step.Stage != "plan" && step.Stage != "dev") || req.Child.Stage != "dev" {
			writeError(w, http.StatusBadRequest, "proposal children must be development work under plan or development")
			return
		}
		agentID, err = parseUUIDValue(req.Child.AgentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "child requires a valid agent_id")
			return
		}
		if status, message := h.authorizeOrchestrationAgentIDs(
			r.Context(), issue, actorType, actorID, []pgtype.UUID{agentID},
		); status != 0 {
			writeError(w, status, message)
			return
		}
		if req.Child.Kind == "" {
			req.Child.Kind = "task"
		}
		if req.Child.Kind != "task" {
			writeError(w, http.StatusBadRequest, "proposal children must be task steps")
			return
		}
		req.Child.Capability = strings.TrimSpace(req.Child.Capability)
		if req.Child.Capability == "" {
			req.Child.Capability = inferredStepCapability(*req.Child)
		}
		if !validOrchestrationCapability(req.Child.Capability) || req.Child.Capability == "coordination" || req.Child.Capability == "integration" || req.Child.Capability == "qa" || req.Child.Capability == "review" || req.Child.Capability == "release" {
			writeError(w, http.StatusBadRequest, "child capability does not match its step kind")
			return
		}
		if routeErr := h.validateOrchestrationAgentRoute(
			r.Context(), issue.WorkspaceID, run.ControllerAgentID, step.SquadID,
			step.ControllerAgentID, agentID, req.Child.Capability,
		); routeErr != nil {
			writeError(w, http.StatusBadRequest, routeErr.Error())
			return
		}
		steps, listErr := h.Queries.ListOrchestrationSteps(r.Context(), run.ID)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "load draft proposal failed")
			return
		}
		for _, candidate := range steps {
			if candidate.StepKind == "integration" && candidate.Stage == "dev" && candidate.Status == "pending" {
				if integrationJoinID.Valid {
					writeError(w, http.StatusConflict, "draft proposal has multiple integration joins")
					return
				}
				integrationJoinID = candidate.ID
				insertPosition = candidate.Position
			}
		}
		if !integrationJoinID.Valid {
			writeError(w, http.StatusConflict, "draft proposal needs a pending integration join before adding parallel work")
			return
		}
		for _, dependencyValue := range req.Child.DependsOnStepIDs {
			dependencyID, parseErr := parseUUIDValue(dependencyValue)
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "invalid child dependency id")
				return
			}
			dependency, dependencyErr := h.Queries.GetOrchestrationStep(r.Context(), dependencyID)
			if dependencyErr != nil || dependency.RunID != run.ID {
				writeError(w, http.StatusBadRequest, "child dependency not found in run")
				return
			}
			if dependency.Stage != "plan" && (dependency.Stage != "dev" || dependency.StepKind != "task") {
				writeError(w, http.StatusBadRequest, "child dependencies must be plan or development tasks")
				return
			}
			if dependency.ID != step.ID {
				extraDependencies = append(extraDependencies, dependency.ID)
			}
		}
	} else if req.Operation == "retire" {
		if run.Status != "draft" {
			writeError(w, http.StatusConflict, "structural plan edits are only allowed on a draft proposal")
			return
		}
		if step.Stage == "release" {
			writeError(w, http.StatusConflict, "the release gate cannot be retired")
			return
		}
		dependencies, listErr := h.Queries.ListOrchestrationStepDependencies(r.Context(), run.ID)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "load draft dependencies failed")
			return
		}
		for _, dependency := range dependencies {
			if dependency.DependsOnStepID == step.ID {
				writeError(w, http.StatusConflict, "step has dependent work; reroute it instead")
				return
			}
		}
	} else {
		writeError(w, http.StatusBadRequest, "operation must be reroute, retire, or add_child")
		return
	}
	if req.Operation == "reroute" || req.Operation == "add_child" {
		candidateSteps, listErr := h.Queries.ListOrchestrationSteps(r.Context(), run.ID)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "load artifact routes failed")
			return
		}
		if req.Operation == "reroute" {
			for index := range candidateSteps {
				if candidateSteps[index].ID == step.ID {
					candidateSteps[index].AgentID = agentID
				}
			}
		}
		futureAgentIDs := orchestrationFutureAgentIDs(candidateSteps, run)
		if req.Operation == "add_child" {
			futureAgentIDs = append(futureAgentIDs, agentID, step.ControllerAgentID)
		}
		if status, message := h.authorizeOrchestrationAgentIDs(
			r.Context(), issue, actorType, actorID, futureAgentIDs,
		); status != 0 {
			writeError(w, status, message)
			return
		}
		artifactAgentIDs := orchestrationArtifactAgentIDs(candidateSteps, run)
		if req.Operation == "add_child" {
			artifactAgentIDs = append(artifactAgentIDs, agentID)
		}
		pinnedLocation := orchestrationArtifactLocationFromPolicy(run)
		if pinnedLocation == "" {
			pinnedLocation, listErr = h.orchestrationPersistedArtifactLocation(r.Context(), candidateSteps)
			if listErr != nil {
				writeError(w, http.StatusConflict, listErr.Error())
				return
			}
		}
		if _, topologyErr := h.validateOrchestrationArtifactRoutes(
			r.Context(), issue, artifactAgentIDs, pinnedLocation,
		); topologyErr != nil {
			writeError(w, http.StatusConflict, topologyErr.Error())
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin plan edit failed")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	advanced, err := qtx.AdvanceOrchestrationPlanVersion(r.Context(), db.AdvanceOrchestrationPlanVersionParams{ID: run.ID, PlanVersion: req.ExpectedVersion})
	if err != nil {
		writeError(w, http.StatusConflict, "plan version changed; reload and retry")
		return
	}
	if req.Operation == "reroute" {
		_, err = qtx.ReroutePendingOrchestrationStep(r.Context(), db.ReroutePendingOrchestrationStepParams{
			ID: step.ID, AgentID: agentID, ModelOverride: rerouteModel,
			ThinkingLevelOverride: rerouteThinkingLevel, Instructions: req.Instructions,
		})
	} else if req.Operation == "retire" {
		_, err = qtx.RetirePendingOrchestrationStep(r.Context(), db.RetirePendingOrchestrationStepParams{ID: step.ID, RetiredInVersion: pgtype.Int4{Int32: advanced.PlanVersion, Valid: true}})
	} else {
		child := req.Child
		maxAttempts := child.MaxAttempts
		if maxAttempts < 1 {
			maxAttempts = 2
		}
		if shiftErr := qtx.StageOrchestrationStepPositionShift(r.Context(), db.StageOrchestrationStepPositionShiftParams{
			RunID: run.ID, Position: insertPosition,
		}); shiftErr != nil {
			err = shiftErr
		} else if shiftErr = qtx.FinishOrchestrationStepPositionShift(r.Context(), run.ID); shiftErr != nil {
			err = shiftErr
		} else {
			childModel, childThinkingLevel, snapshotErr := orchestrationExecutionSnapshot(
				r.Context(), qtx, issue.WorkspaceID, agentID, child.Model,
			)
			if snapshotErr != nil {
				err = snapshotErr
			} else {
				created, createErr := qtx.CreateOrchestrationStep(r.Context(), db.CreateOrchestrationStepParams{
					RunID: run.ID, StepKey: strings.TrimSpace(child.Key), Title: strings.TrimSpace(child.Title), Stage: child.Stage,
					Position: insertPosition, AgentID: agentID, ModelOverride: childModel, ThinkingLevelOverride: childThinkingLevel,
					DependsOnStepID: step.ID, ApprovalRequired: child.ApprovalRequired, MaxAttempts: maxAttempts, Instructions: strings.TrimSpace(child.Instructions),
					ParentStepID: step.ID, SquadID: step.SquadID, ControllerAgentID: step.ControllerAgentID, IntroducedInVersion: advanced.PlanVersion, StepKind: child.Kind,
					Capability: child.Capability,
				})
				if createErr != nil {
					err = createErr
				} else {
					err = qtx.AddOrchestrationStepDependency(r.Context(), db.AddOrchestrationStepDependencyParams{StepID: created.ID, DependsOnStepID: step.ID})
					for _, dependencyID := range extraDependencies {
						if err != nil {
							break
						}
						err = qtx.AddOrchestrationStepDependency(r.Context(), db.AddOrchestrationStepDependencyParams{StepID: created.ID, DependsOnStepID: dependencyID})
					}
					if err == nil {
						err = qtx.AddOrchestrationStepDependency(r.Context(), db.AddOrchestrationStepDependencyParams{StepID: integrationJoinID, DependsOnStepID: created.ID})
					}
				}
			}
		}
	}
	if err != nil {
		writeError(w, http.StatusConflict, "only pending steps can be edited")
		return
	}
	patch, _ := json.Marshal(req)
	_, err = qtx.CreateOrchestrationPlanRevision(r.Context(), db.CreateOrchestrationPlanRevisionParams{RunID: run.ID, Version: advanced.PlanVersion, ActorType: "member", ActorID: parseUUID(userID), Reason: req.Reason, Patch: patch})
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "save plan revision failed")
		return
	}
	h.createOrchestrationEvent(r.Context(), run.ID, step.ID, "plan_revised", "member", parseUUID(userID), map[string]any{"version": advanced.PlanVersion, "operation": req.Operation, "reason": req.Reason})
	// Editing a review-first proposal must never start it as a side effect.
	// Running plans can immediately reconsider newly-routed pending work; draft
	// plans move only through the explicit Start action.
	if run.Status != "draft" {
		_ = h.dispatchNextOrchestrationStep(r.Context(), run.ID, issue)
	}
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusOK)
}

func (h *Handler) StartIssueOrchestration(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	run, err := h.Queries.GetActiveOrchestrationRunForIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no active orchestration")
		return
	}
	if !h.authorizeOrchestrationRunRequest(w, r, issue, run, userID) {
		return
	}
	if run, err = setManualOrchestrationDispatchAuthorization(r.Context(), h.Queries, run, true); err != nil {
		writeError(w, http.StatusConflict, "orchestration cannot be continued from its current state")
		return
	}
	if err := h.dispatchNextOrchestrationStep(r.Context(), run.ID, issue); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusAccepted)
}

func (h *Handler) ApproveOrchestrationStep(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	stepID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "stepId"), "step_id")
	if !ok {
		return
	}
	step, err := h.Queries.GetOrchestrationStep(r.Context(), stepID)
	if err != nil {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	run, err := h.Queries.GetOrchestrationRun(r.Context(), step.RunID)
	if err != nil || run.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	if !h.authorizeOrchestrationRunRequest(w, r, issue, run, userID) {
		return
	}
	step, err = h.approvePersistedOrchestrationStep(r.Context(), issue, run, step, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if step.Stage == "release" {
		userName := "a teammate"
		if user, userErr := h.Queries.GetUser(r.Context(), parseUUID(userID)); userErr == nil && strings.TrimSpace(user.Name) != "" {
			userName = user.Name
		}
		h.recordReleaseApproval(r, issue, userID, userName, "")
	}
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusOK)
}

// approvePersistedOrchestrationStep is the single approval transition used by
// both the orchestration API and the compatibility review-decision endpoint.
// A routed approval gate becomes executable work; an intentionally human-only
// gate completes immediately. Release always re-checks deterministic gates.
func (h *Handler) approvePersistedOrchestrationStep(ctx context.Context, issue db.Issue, run db.OrchestrationRun, step db.OrchestrationStep, approvedBy pgtype.UUID) (db.OrchestrationStep, error) {
	if step.Status != "waiting_approval" && !step.ApprovedAt.Valid {
		return step, fmt.Errorf("step is not waiting for approval")
	}
	approved := step
	var approvalEvent *db.OrchestrationEvent
	if h.TxStarter == nil {
		if err := h.checkReleaseOrchestrationApproval(ctx, issue, step); err != nil {
			return step, err
		}
		var err error
		if step.Status == "waiting_approval" {
			approved, err = h.Queries.ApproveOrchestrationStep(ctx, db.ApproveOrchestrationStepParams{ID: step.ID, ApprovedBy: approvedBy})
			if err != nil {
				return step, fmt.Errorf("step is not waiting for approval")
			}
			h.createOrchestrationEvent(ctx, run.ID, approved.ID, "step_approved", "member", approvedBy, nil)
		}
		if run, err = setManualOrchestrationDispatchAuthorization(ctx, h.Queries, run, true); err != nil {
			return approved, fmt.Errorf("authorize approved orchestration step: %w", err)
		}
		if _, _, err = h.reconcileOrchestrationRunLifecycle(ctx, run.ID, "running"); err != nil {
			return approved, fmt.Errorf("reconcile approved orchestration: %w", err)
		}
	} else {
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return step, fmt.Errorf("begin orchestration approval: %w", err)
		}
		defer tx.Rollback(ctx)
		qtx := h.Queries.WithTx(tx)
		lockedRun, err := qtx.LockOrchestrationRun(ctx, run.ID)
		if err != nil || lockedRun.IssueID != issue.ID || orchestrationRunStatusIsTerminal(lockedRun.Status) {
			return step, fmt.Errorf("orchestration run changed")
		}
		current, err := qtx.GetOrchestrationStep(ctx, step.ID)
		if err != nil || current.RunID != lockedRun.ID || (current.Status != "waiting_approval" && !current.ApprovedAt.Valid) {
			return step, fmt.Errorf("step is not waiting for approval")
		}
		if err = h.checkReleaseOrchestrationApproval(ctx, issue, current); err != nil {
			return current, err
		}
		approved = current
		if current.Status == "waiting_approval" {
			approved, err = qtx.ApproveOrchestrationStep(ctx, db.ApproveOrchestrationStepParams{ID: current.ID, ApprovedBy: approvedBy})
			if err != nil {
				return current, fmt.Errorf("step is not waiting for approval")
			}
			event, eventErr := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
				RunID: lockedRun.ID, StepID: approved.ID, Kind: "step_approved", ActorType: "member", ActorID: approvedBy, Details: []byte(`{}`),
			})
			if eventErr != nil {
				return current, eventErr
			}
			approvalEvent = &event
		}
		lockedRun, err = setManualOrchestrationDispatchAuthorization(ctx, qtx, lockedRun, true)
		if err != nil {
			return current, fmt.Errorf("authorize approved orchestration step: %w", err)
		}
		if _, _, err = setOrchestrationRunLifecycleStatus(ctx, qtx, lockedRun.ID, "running"); err != nil {
			return approved, fmt.Errorf("reconcile approved orchestration: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return approved, fmt.Errorf("commit orchestration approval: %w", err)
		}
		run = lockedRun
		if approvalEvent != nil {
			h.publishOrchestrationChanged(ctx, run, *approvalEvent)
		}
	}
	if dispatchErr := h.dispatchNextOrchestrationStep(ctx, run.ID, issue); dispatchErr != nil {
		if approved.Status == "pending" {
			_ = h.restoreOrchestrationApprovalAfterDispatchFailure(ctx, run, approved)
		}
		return approved, fmt.Errorf("dispatch approved orchestration step: %w", dispatchErr)
	}
	return approved, nil
}

func (h *Handler) checkReleaseOrchestrationApproval(ctx context.Context, issue db.Issue, step db.OrchestrationStep) error {
	if step.Status != "waiting_approval" || step.Stage != "release" || h.issueHasLabel(ctx, issue, sprintPRMergeOverrideLabel) {
		return nil
	}
	readiness := h.computeMergeReadiness(ctx, issue)
	if readiness.Ready {
		return nil
	}
	reason := strings.Join(readiness.Blocked, "; ")
	if reason == "" {
		reason = "one or more required gates have not passed"
	}
	return fmt.Errorf("merge_gates_not_satisfied: %s", reason)
}

func (h *Handler) restoreOrchestrationApprovalAfterDispatchFailure(ctx context.Context, run db.OrchestrationRun, step db.OrchestrationStep) error {
	if h.TxStarter == nil {
		if _, err := h.Queries.WaitOrchestrationStepApproval(ctx, step.ID); err != nil {
			return err
		}
		if _, err := setManualOrchestrationDispatchAuthorization(ctx, h.Queries, run, false); err != nil {
			return err
		}
		_, _, err := h.reconcileOrchestrationRunLifecycle(ctx, run.ID, "waiting_approval")
		return err
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	lockedRun, err := qtx.LockOrchestrationRun(ctx, run.ID)
	if err != nil {
		return err
	}
	current, err := qtx.GetOrchestrationStep(ctx, step.ID)
	if err != nil || current.Status != "pending" {
		return err
	}
	if _, err = qtx.WaitOrchestrationStepApproval(ctx, current.ID); err != nil {
		return err
	}
	if lockedRun, err = setManualOrchestrationDispatchAuthorization(ctx, qtx, lockedRun, false); err != nil {
		return err
	}
	if _, _, err = setOrchestrationRunLifecycleStatus(ctx, qtx, lockedRun.ID, "waiting_approval"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (h *Handler) RetryOrchestrationStep(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	stepID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "stepId"), "step_id")
	if !ok {
		return
	}
	step, err := h.Queries.GetOrchestrationStep(r.Context(), stepID)
	if err != nil {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	run, err := h.Queries.GetOrchestrationRun(r.Context(), step.RunID)
	if err != nil || run.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	if !h.authorizeOrchestrationRunRequest(w, r, issue, run, userID) {
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusInternalServerError, "orchestration transactions are not configured")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not begin orchestration retry")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	lockedRun, err := qtx.LockOrchestrationRun(r.Context(), run.ID)
	if err != nil || lockedRun.IssueID != issue.ID {
		writeError(w, http.StatusConflict, "orchestration run changed")
		return
	}
	current, err := qtx.GetOrchestrationStep(r.Context(), stepID)
	if err != nil || current.RunID != lockedRun.ID {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	var event db.OrchestrationEvent
	newRetry := false
	if current.Status == "failed" || current.Status == "blocked" {
		current, err = qtx.ResetOrchestrationStepForRetry(r.Context(), stepID)
		if err != nil {
			writeError(w, http.StatusConflict, "step has no retries remaining")
			return
		}
		lockedRun, err = qtx.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: lockedRun.ID, Status: "running"})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not reopen orchestration run")
			return
		}
		event, err = qtx.CreateOrchestrationEvent(r.Context(), db.CreateOrchestrationEventParams{
			RunID: lockedRun.ID, StepID: current.ID, Kind: "step_retry_requested", ActorType: "member",
			ActorID: parseUUID(userID), Details: []byte(`{}`),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not record orchestration retry")
			return
		}
		newRetry = true
	} else if lockedRun.Status != "running" || (current.Status != "pending" && current.Status != "queued" && current.Status != "running") {
		writeError(w, http.StatusConflict, "step has no retries remaining")
		return
	}
	lockedRun, err = setManualOrchestrationDispatchAuthorization(r.Context(), qtx, lockedRun, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not authorize orchestration retry")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit orchestration retry")
		return
	}
	step = current
	if newRetry {
		h.publishOrchestrationChanged(r.Context(), lockedRun, event)
	}
	if err := h.dispatchNextOrchestrationStep(r.Context(), lockedRun.ID, issue); err != nil {
		writeError(w, http.StatusServiceUnavailable, "retry saved; orchestration dispatch will be retried")
		return
	}
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusAccepted)
}

type orchestrationStepResponseRequest struct {
	Message    string `json:"message"`
	QuestionID string `json:"question_id"`
}

// RespondToOrchestrationStep records a durable answer to a blocking agent
// question and schedules a continuation of the same work unit. The answer is
// injected through the orchestration message log; it does not create an
// out-of-DAG mention task.
func (h *Handler) RespondToOrchestrationStep(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	stepID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "stepId"), "step_id")
	if !ok {
		return
	}
	var req orchestrationStepResponseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || len(req.Message) > 8000 {
		writeError(w, http.StatusBadRequest, "message is required and must be at most 8000 bytes")
		return
	}
	questionID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.QuestionID), "question_id")
	if !ok {
		return
	}
	step, err := h.Queries.GetOrchestrationStep(r.Context(), stepID)
	if err != nil {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	run, err := h.Queries.GetOrchestrationRun(r.Context(), step.RunID)
	if err != nil || run.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	if !h.authorizeOrchestrationRunRequest(w, r, issue, run, userID) {
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusInternalServerError, "orchestration transactions are not configured")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not begin orchestration response")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	run, err = qtx.LockOrchestrationRun(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not lock orchestration run")
		return
	}
	if orchestrationRunStatusIsTerminal(run.Status) {
		writeError(w, http.StatusConflict, "orchestration run is already finished")
		return
	}
	step, err = qtx.GetOrchestrationStep(r.Context(), step.ID)
	if err != nil || step.RunID != run.ID {
		writeError(w, http.StatusConflict, "orchestration step changed")
		return
	}
	question, err := qtx.GetOrchestrationQuestionForUpdate(r.Context(), db.GetOrchestrationQuestionForUpdateParams{
		ID: questionID, StepID: step.ID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "question does not match this orchestration step")
		return
	}
	if question.RunID != run.ID {
		writeError(w, http.StatusConflict, "question does not match this orchestration run")
		return
	}
	latestQuestion, err := qtx.GetLatestOrchestrationQuestion(r.Context(), step.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "step is not waiting for a response")
		return
	}
	body, _ := json.Marshal(map[string]any{"message": req.Message})
	digest := sha256.Sum256([]byte(userID + "\x00" + req.Message))
	idempotencyKey := fmt.Sprintf("answer:%s:%x", uuidToString(question.ID), digest[:])
	if question.ResolvedAt.Valid {
		// A client may retry after the answer committed but the HTTP response was
		// lost (or downstream dispatch briefly failed). Only the exact same
		// member/message replay is accepted; a different answer conflicts.
		if _, replayErr := qtx.GetOrchestrationMessageByIdempotencyKey(r.Context(), db.GetOrchestrationMessageByIdempotencyKeyParams{
			RunID: run.ID, IdempotencyKey: idempotencyKey,
		}); replayErr != nil {
			writeError(w, http.StatusConflict, "step response has already been resolved")
			return
		}
		// A later clarification may already be open. The exact Q1 answer remains
		// an accepted idempotent replay, but it must not authorize or dispatch
		// work associated with Q2.
		if latestQuestion.ID != question.ID {
			if rollbackErr := tx.Rollback(r.Context()); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				writeError(w, http.StatusInternalServerError, "could not release orchestration response")
				return
			}
			h.writeIssueOrchestration(w, r, issue.ID, http.StatusAccepted)
			return
		}
		if run.ProgressionPolicy == "manual" {
			run, err = setManualOrchestrationDispatchAuthorization(r.Context(), qtx, run, true)
			if err == nil {
				_, _, err = setOrchestrationRunLifecycleStatus(r.Context(), qtx, run.ID, "running")
			}
			if err == nil {
				err = tx.Commit(r.Context())
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "could not authorize saved orchestration response")
				return
			}
		} else if rollbackErr := tx.Rollback(r.Context()); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			writeError(w, http.StatusInternalServerError, "could not release orchestration response")
			return
		}
		if err := h.dispatchNextOrchestrationStep(r.Context(), run.ID, issue); err != nil {
			writeError(w, http.StatusInternalServerError, "response saved but continuation could not be dispatched")
			return
		}
		h.writeIssueOrchestration(w, r, issue.ID, http.StatusAccepted)
		return
	}
	if latestQuestion.ID != question.ID {
		writeError(w, http.StatusConflict, "question is no longer the active orchestration question")
		return
	}
	answer, err := qtx.CreateOrchestrationMessage(r.Context(), db.CreateOrchestrationMessageParams{
		RunID: run.ID, StepID: step.ID, Kind: "answer", ActorType: "member", ActorID: parseUUID(userID),
		TargetType: "agent", TargetID: step.AgentID, Body: body, PlanVersion: run.PlanVersion,
		CorrelationID: question.CorrelationID, CausationID: question.ID, ReplyToID: question.ID,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record orchestration response")
		return
	}
	if _, err = qtx.ResolveOrchestrationMessage(r.Context(), question.ID); err != nil {
		writeError(w, http.StatusConflict, "orchestration question was answered concurrently")
		return
	}
	step, err = qtx.ResumeOrchestrationStepAfterInput(r.Context(), step.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "step is no longer waiting for a response")
		return
	}
	if _, _, err = setOrchestrationRunLifecycleStatus(r.Context(), qtx, run.ID, "running"); err != nil {
		writeError(w, http.StatusInternalServerError, "could not resume orchestration run")
		return
	}
	run, err = setManualOrchestrationDispatchAuthorization(r.Context(), qtx, run, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not authorize orchestration continuation")
		return
	}
	eventDetails, _ := json.Marshal(map[string]any{
		"message_id": uuidToString(answer.ID), "question_id": uuidToString(question.ID),
	})
	event, err := qtx.CreateOrchestrationEvent(r.Context(), db.CreateOrchestrationEventParams{
		RunID: run.ID, StepID: step.ID, Kind: "input_received", ActorType: "member",
		ActorID: parseUUID(userID), Details: eventDetails,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record orchestration response event")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit orchestration response")
		return
	}
	h.publishOrchestrationChanged(r.Context(), run, event)
	if err := h.dispatchNextOrchestrationStep(r.Context(), run.ID, issue); err != nil {
		writeError(w, http.StatusInternalServerError, "response saved but continuation could not be dispatched")
		return
	}
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusAccepted)
}

// respondToOrchestrationQuestionFromComment turns an explicit member mention
// into the same durable answer/resume operation as the orchestration response
// endpoint. It never enqueues a normal mention task: dispatching the resumed
// step preserves orchestration_step_id, which is the provider-session lineage
// key used by the daemon claim path.
func (h *Handler) respondToOrchestrationQuestionFromComment(ctx context.Context, issue db.Issue, trigger commentAgentTrigger, commentID pgtype.UUID) error {
	if h.TxStarter == nil {
		return fmt.Errorf("orchestration transactions are not configured")
	}
	if !trigger.StepID.Valid || !trigger.QuestionID.Valid {
		return fmt.Errorf("exact orchestration question target is not configured")
	}
	comment, err := h.Queries.GetComment(ctx, commentID)
	if err != nil {
		return fmt.Errorf("load answer comment: %w", err)
	}
	if comment.AuthorType != "member" || comment.IssueID != issue.ID || comment.WorkspaceID != issue.WorkspaceID {
		return fmt.Errorf("comment is not a member answer on this issue")
	}
	message := strings.TrimSpace(comment.Content)
	if message == "" {
		return fmt.Errorf("answer comment is empty")
	}

	step, err := h.Queries.GetOrchestrationStep(ctx, trigger.StepID)
	if err != nil {
		return fmt.Errorf("load orchestration step: %w", err)
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin orchestration comment response: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	run, err := qtx.LockOrchestrationRun(ctx, step.RunID)
	if err != nil {
		return fmt.Errorf("lock orchestration run: %w", err)
	}
	if run.IssueID != issue.ID || run.WorkspaceID != issue.WorkspaceID || orchestrationRunStatusIsTerminal(run.Status) {
		return fmt.Errorf("orchestration run changed")
	}
	step, err = qtx.GetOrchestrationStep(ctx, trigger.StepID)
	if err != nil || step.RunID != run.ID || step.AgentID != trigger.Agent.ID {
		return fmt.Errorf("orchestration step changed")
	}
	futureSteps, err := qtx.ListOrchestrationSteps(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("load orchestration routes for answer: %w", err)
	}
	if status, authorizationMessage := h.authorizeOrchestrationAgentIDs(
		ctx, issue, "member", uuidToString(comment.AuthorID), orchestrationFutureAgentIDs(futureSteps, run),
	); status != 0 {
		return fmt.Errorf("orchestration answer is not authorized: %s", authorizationMessage)
	}

	idempotencyKey := "comment-answer:" + uuidToString(comment.ID)
	existing, replayErr := qtx.GetOrchestrationMessageByIdempotencyKey(ctx, db.GetOrchestrationMessageByIdempotencyKeyParams{
		RunID: run.ID, IdempotencyKey: idempotencyKey,
	})
	if replayErr == nil {
		if existing.StepID != step.ID || existing.ActorType != "member" || existing.ActorID != comment.AuthorID ||
			existing.CausationID != trigger.QuestionID {
			return fmt.Errorf("comment answer idempotency key belongs to another response")
		}
		// No durable state needs changing on an exact replay. If the original
		// transaction committed immediately before dispatch failed, the pending
		// step remains eligible for the same idempotent dispatcher repair.
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return fmt.Errorf("release orchestration comment replay: %w", rollbackErr)
		}
		if step.Status == "pending" && run.Status == "running" {
			if err := h.dispatchNextOrchestrationStep(ctx, run.ID, issue); err != nil {
				return fmt.Errorf("dispatch saved orchestration comment response: %w", err)
			}
		}
		return nil
	}
	if !errors.Is(replayErr, pgx.ErrNoRows) {
		return fmt.Errorf("check orchestration comment response replay: %w", replayErr)
	}
	if step.Status != "waiting_input" {
		return fmt.Errorf("orchestration step is no longer waiting for input")
	}

	question, err := qtx.GetOrchestrationQuestionForUpdate(ctx, db.GetOrchestrationQuestionForUpdateParams{
		ID: trigger.QuestionID, StepID: step.ID,
	})
	if err != nil || question.RunID != run.ID || question.ResolvedAt.Valid {
		return fmt.Errorf("orchestration question is no longer open")
	}
	latestQuestion, err := qtx.GetLatestOrchestrationQuestion(ctx, step.ID)
	if err != nil || latestQuestion.ID != question.ID {
		return fmt.Errorf("orchestration question is no longer active")
	}
	body, _ := json.Marshal(map[string]any{
		"message": message, "comment_id": uuidToString(comment.ID), "source": "agent_mention",
	})
	answer, err := qtx.CreateOrchestrationMessage(ctx, db.CreateOrchestrationMessageParams{
		RunID: run.ID, StepID: step.ID, Kind: "answer", ActorType: "member", ActorID: comment.AuthorID,
		TargetType: "agent", TargetID: step.AgentID, Body: body, PlanVersion: run.PlanVersion,
		CorrelationID: question.CorrelationID, CausationID: question.ID, ReplyToID: question.ID,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return fmt.Errorf("record orchestration comment response: %w", err)
	}
	if _, err = qtx.ResolveOrchestrationMessage(ctx, question.ID); err != nil {
		return fmt.Errorf("resolve orchestration question: %w", err)
	}
	step, err = qtx.ResumeOrchestrationStepAfterInput(ctx, step.ID)
	if err != nil {
		return fmt.Errorf("resume orchestration step: %w", err)
	}
	if _, _, err = setOrchestrationRunLifecycleStatus(ctx, qtx, run.ID, "running"); err != nil {
		return fmt.Errorf("resume orchestration run: %w", err)
	}
	run, err = setManualOrchestrationDispatchAuthorization(ctx, qtx, run, true)
	if err != nil {
		return fmt.Errorf("authorize orchestration continuation: %w", err)
	}
	eventDetails, _ := json.Marshal(map[string]any{
		"message_id": uuidToString(answer.ID), "question_id": uuidToString(question.ID), "comment_id": uuidToString(comment.ID),
	})
	event, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		RunID: run.ID, StepID: step.ID, Kind: "input_received", ActorType: "member",
		ActorID: comment.AuthorID, Details: eventDetails,
	})
	if err != nil {
		return fmt.Errorf("record orchestration comment response event: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit orchestration comment response: %w", err)
	}
	h.publishOrchestrationChanged(ctx, run, event)
	if err := h.dispatchNextOrchestrationStep(ctx, run.ID, issue); err != nil {
		return fmt.Errorf("response saved but continuation could not be dispatched: %w", err)
	}
	return nil
}

func (h *Handler) CancelOrchestrationBranch(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	stepID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "stepId"), "step_id")
	if !ok {
		return
	}
	root, err := h.Queries.GetOrchestrationStep(r.Context(), stepID)
	if err != nil {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	run, err := h.Queries.GetOrchestrationRun(r.Context(), root.RunID)
	if err != nil || run.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	branch, err := h.Queries.ListOrchestrationBranchSteps(r.Context(), stepID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load branch failed")
		return
	}
	cancelled := 0
	for _, step := range branch {
		if _, cancelErr := h.Queries.CancelOrchestrationStep(r.Context(), step.ID); cancelErr != nil {
			continue
		}
		cancelled++
		h.createOrchestrationEvent(r.Context(), run.ID, step.ID, "step_cancelled", "member", parseUUID(requestUserID(r)), map[string]any{"branch_root_id": uuidToString(stepID)})
		if step.TaskID.Valid {
			_, _ = h.TaskService.CancelTaskWithResult(r.Context(), step.TaskID)
		}
	}
	if cancelled == 0 {
		writeError(w, http.StatusConflict, "branch has no cancellable steps")
		return
	}
	h.createOrchestrationEvent(r.Context(), run.ID, stepID, "branch_cancelled", "member", parseUUID(requestUserID(r)), map[string]any{"cancelled_steps": cancelled})
	_ = h.dispatchNextOrchestrationStep(r.Context(), run.ID, issue)
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusOK)
}

// orchestrationTerminalRunStatus makes an exhausted failure immediately fatal,
// then distinguishes a successful run from a branch cancellation that removed
// its mandatory release path. Cancelled optional work may still converge into
// a completed release, but a cancelled release (or any cancellation in a
// custom plan without a release gate) cannot be presented as success.
func orchestrationTerminalRunStatus(steps []db.OrchestrationStep) (string, bool) {
	if len(steps) == 0 {
		return "", false
	}
	for _, step := range steps {
		if step.Status == "failed" && step.Attempt >= step.MaxAttempts {
			return "failed", true
		}
	}
	hasRelease, releaseSatisfied, anyCancelled := false, false, false
	for _, step := range steps {
		terminal := step.Status == "completed" || step.Status == "skipped" || step.Status == "cancelled"
		if !terminal {
			return "", false
		}
		anyCancelled = anyCancelled || step.Status == "cancelled"
		if step.Stage == "release" {
			hasRelease = true
			releaseSatisfied = releaseSatisfied || step.Status == "completed" || step.Status == "skipped"
		}
	}
	if (hasRelease && !releaseSatisfied) || (!hasRelease && anyCancelled) {
		return "cancelled", true
	}
	return "completed", true
}

func orchestrationRunStatusIsTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

// orchestrationRunLifecycleStatus is the single precedence rule for projecting
// persisted step state onto a non-draft run. A blocker is the strongest
// attention signal, followed by a blocking question. Approval only becomes the
// run status once agent work is idle; independent active branches otherwise
// keep the run visibly running.
func orchestrationRunLifecycleStatus(steps []db.OrchestrationStep, idleStatus string) (string, bool) {
	if terminalStatus, terminal := orchestrationTerminalRunStatus(steps); terminal {
		return terminalStatus, true
	}
	waitingApproval, waitingInput, blocked, hasActive := false, false, false, false
	for _, step := range steps {
		waitingApproval = waitingApproval || step.Status == "waiting_approval"
		waitingInput = waitingInput || step.Status == "waiting_input"
		blocked = blocked || step.Status == "blocked"
		hasActive = hasActive || step.Status == "queued" || step.Status == "running"
	}
	switch {
	case blocked:
		return "blocked", false
	case waitingInput:
		return "waiting_input", false
	case waitingApproval && !hasActive:
		return "waiting_approval", false
	case hasActive:
		return "running", false
	default:
		return idleStatus, false
	}
}

func setOrchestrationRunLifecycleStatus(ctx context.Context, queries *db.Queries, runID pgtype.UUID, idleStatus string) (string, bool, error) {
	steps, err := queries.ListOrchestrationSteps(ctx, runID)
	if err != nil {
		return "", false, err
	}
	status, terminal := orchestrationRunLifecycleStatus(steps, idleStatus)
	if _, err = queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: status}); err != nil {
		return "", false, err
	}
	return status, terminal, nil
}

// reconcileOrchestrationRunLifecycle serializes status reduction through the
// run row. Without the lock, two parallel branch completions can each observe
// the other branch's old state and let a weaker waiting_input/approval status
// overwrite a committed blocker.
func (h *Handler) reconcileOrchestrationRunLifecycle(ctx context.Context, runID pgtype.UUID, idleStatus string) (string, bool, error) {
	previousStatus := ""
	if h.TxStarter == nil {
		run, err := h.Queries.GetOrchestrationRun(ctx, runID)
		if err != nil {
			return "", false, err
		}
		previousStatus = run.Status
		if orchestrationRunStatusIsTerminal(previousStatus) {
			return previousStatus, true, nil
		}
		status, terminal, err := setOrchestrationRunLifecycleStatus(ctx, h.Queries, runID, idleStatus)
		if err == nil && terminal && previousStatus != status {
			h.createOrchestrationEvent(ctx, runID, pgtype.UUID{}, "run_"+status, "system", pgtype.UUID{}, nil)
		}
		return status, terminal, err
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	lockedRun, err := h.Queries.WithTx(tx).LockOrchestrationRun(ctx, runID)
	if err != nil {
		return "", false, err
	}
	previousStatus = lockedRun.Status
	if orchestrationRunStatusIsTerminal(previousStatus) {
		return previousStatus, true, nil
	}
	status, terminal, err := setOrchestrationRunLifecycleStatus(ctx, h.Queries.WithTx(tx), runID, idleStatus)
	if err != nil {
		return "", false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", false, err
	}
	if terminal && previousStatus != status {
		h.createOrchestrationEvent(ctx, runID, pgtype.UUID{}, "run_"+status, "system", pgtype.UUID{}, nil)
	}
	return status, terminal, nil
}

// failOrchestrationRunUnlessTerminal serializes cancellation against sibling
// completion. A late cancellation may fail an active run, but it must never
// rewrite a run that already committed a terminal outcome.
func (h *Handler) failOrchestrationRunUnlessTerminal(ctx context.Context, runID pgtype.UUID) error {
	if h.TxStarter == nil {
		run, err := h.Queries.GetOrchestrationRun(ctx, runID)
		if err != nil || orchestrationRunStatusIsTerminal(run.Status) {
			return err
		}
		if _, err = h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: "failed"}); err != nil {
			return err
		}
		h.createOrchestrationEvent(ctx, runID, pgtype.UUID{}, "run_failed", "system", pgtype.UUID{}, map[string]any{"reason": "task_cancelled"})
		return nil
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	run, err := qtx.LockOrchestrationRun(ctx, runID)
	if err != nil {
		return err
	}
	if orchestrationRunStatusIsTerminal(run.Status) {
		return nil
	}
	failedRun, err := qtx.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: "failed"})
	if err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"reason": "task_cancelled"})
	event, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		RunID: runID, Kind: "run_failed", ActorType: "system", Details: details,
	})
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	h.publishOrchestrationChanged(ctx, failedRun, event)
	return nil
}

var (
	errOrchestrationDispatchBusy       = errors.New("orchestration dispatch agent is busy")
	errOrchestrationDispatchAtCapacity = errors.New("orchestration run is at capacity")
	errOrchestrationDispatchSuperseded = errors.New("orchestration step is no longer dispatchable")
	errOrchestrationArtifactMoved      = errors.New("orchestration artifact location changed")
)

// queueOrchestrationStepAtomically closes the crash windows between charging
// an orchestration attempt, inserting its task, and linking that task back to
// the step. No daemon is notified until the transaction commits, so every
// visible queued task has a durable owning step and every charged attempt has a
// task generation.
func (h *Handler) queueOrchestrationStepAtomically(
	ctx context.Context,
	runID pgtype.UUID,
	issue db.Issue,
	step db.OrchestrationStep,
	artifactLocation string,
	maxConcurrency int,
) (db.AgentTaskQueue, db.OrchestrationEvent, error) {
	if h.TxStarter == nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, fmt.Errorf("orchestration transactions are not configured")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	lockedRun, err := qtx.LockOrchestrationRun(ctx, runID)
	if err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	if orchestrationRunStatusIsTerminal(lockedRun.Status) || lockedRun.Status == "draft" || !manualOrchestrationDispatchAuthorized(lockedRun) {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, errOrchestrationDispatchSuperseded
	}
	lockedSteps, err := qtx.ListOrchestrationSteps(ctx, lockedRun.ID)
	if err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	active := 0
	activeAgents := make(map[pgtype.UUID]struct{})
	for _, candidate := range lockedSteps {
		if candidate.Status == "queued" || candidate.Status == "running" {
			active++
			if candidate.AgentID.Valid {
				activeAgents[candidate.AgentID] = struct{}{}
			}
		}
	}
	if maxConcurrency < 1 || active >= maxConcurrency {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, errOrchestrationDispatchAtCapacity
	}
	current, err := qtx.GetOrchestrationStep(ctx, step.ID)
	if err != nil || current.RunID != lockedRun.ID || current.Status != "pending" || current.AgentID != step.AgentID {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, errOrchestrationDispatchSuperseded
	}
	if _, occupied := activeAgents[current.AgentID]; occupied {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, errOrchestrationDispatchBusy
	}
	lockedRunnable, err := qtx.ListRunnableOrchestrationSteps(ctx, lockedRun.ID)
	if err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	stillRunnable := false
	for _, candidate := range lockedRunnable {
		if candidate.ID == current.ID {
			stillRunnable = true
			break
		}
	}
	if !stillRunnable {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, errOrchestrationDispatchSuperseded
	}
	busy, err := qtx.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
		IssueID: issue.ID, AgentID: current.AgentID,
	})
	if err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	if busy {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, errOrchestrationDispatchBusy
	}
	if _, err = qtx.QueueOrchestrationStep(ctx, current.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.AgentTaskQueue{}, db.OrchestrationEvent{}, errOrchestrationDispatchSuperseded
		}
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	task, err := h.TaskService.CreateOrchestrationTaskInTx(
		ctx, qtx, issue, current.AgentID, current.ID,
		current.ModelOverride, current.ThinkingLevelOverride,
		current.Attempt > 0 && (current.Stage == "qa" || current.Stage == "review"),
	)
	if err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	if current.Stage != "plan" && artifactLocation != "" {
		actualLocation, locationErr := h.orchestrationRuntimeArtifactLocation(ctx, task.RuntimeID)
		if locationErr != nil {
			return task, db.OrchestrationEvent{}, locationErr
		}
		if actualLocation != artifactLocation {
			return task, db.OrchestrationEvent{}, fmt.Errorf("%w: expected %s, got %s", errOrchestrationArtifactMoved, artifactLocation, actualLocation)
		}
	}
	if _, err = qtx.AttachTaskToOrchestrationStep(ctx, db.AttachTaskToOrchestrationStepParams{
		ID: current.ID, TaskID: task.ID,
	}); err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	details, _ := json.Marshal(map[string]any{
		"task_id": uuidToString(task.ID), "model": current.ModelOverride.String,
		"thinking_level": current.ThinkingLevelOverride.String,
	})
	event, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		RunID: lockedRun.ID, StepID: current.ID, Kind: "step_queued", ActorType: "agent",
		ActorID: current.AgentID, Details: details,
	})
	if err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return db.AgentTaskQueue{}, db.OrchestrationEvent{}, err
	}
	return task, event, nil
}

func (h *Handler) dispatchNextOrchestrationStep(ctx context.Context, runID pgtype.UUID, issue db.Issue) error {
	run, err := h.Queries.GetOrchestrationRun(ctx, runID)
	if err != nil {
		return err
	}
	// Manual progression is fail-closed. Only explicit Start/answer/retry/
	// approval actions set this durable bit; task completion clears it before
	// any newly-ready stage can be considered. This makes sweeper replays safe
	// without accidentally crossing a human batch boundary.
	if !manualOrchestrationDispatchAuthorized(run) {
		return nil
	}
	if run.Status == "draft" {
		if _, err = h.Queries.StartOrchestrationRun(ctx, runID); err != nil {
			return err
		}
	}
	if run.Status == "failed" || run.Status == "cancelled" || run.Status == "completed" {
		return nil
	}
	steps, err := h.Queries.ListOrchestrationSteps(ctx, runID)
	if err != nil {
		return err
	}
	artifactLocation := orchestrationArtifactLocationFromPolicy(run)
	if artifactLocation == "" {
		artifactLocation, err = h.orchestrationPersistedArtifactLocation(ctx, steps)
		if err != nil {
			return err
		}
		validatedLocation, validateErr := h.validateOrchestrationArtifactRoutes(
			ctx, issue, orchestrationArtifactAgentIDs(steps, run), artifactLocation,
		)
		if validateErr != nil {
			return validateErr
		}
		if artifactLocation == "" {
			artifactLocation = validatedLocation
		}
		if artifactLocation != "" {
			run, err = h.Queries.PinOrchestrationArtifactLocation(ctx, db.PinOrchestrationArtifactLocationParams{
				ID: run.ID, Location: artifactLocation,
			})
			if err != nil {
				return fmt.Errorf("pin orchestration artifact location: %w", err)
			}
		}
	}
	maxConcurrency := 3
	var policy map[string]any
	if json.Unmarshal(run.Policy, &policy) == nil {
		if configured, ok := policy["max_concurrency"].(float64); ok && configured >= 1 && configured <= 10 {
			maxConcurrency = int(configured)
		}
	}
	runnable, err := h.Queries.ListRunnableOrchestrationSteps(ctx, runID)
	if err != nil {
		return err
	}
	for _, step := range runnable {
		if step.ApprovalRequired && !step.AgentID.Valid {
			if _, waitErr := h.Queries.WaitOrchestrationStepApproval(ctx, step.ID); waitErr == nil {
				h.createOrchestrationEvent(ctx, runID, step.ID, "approval_requested", "system", pgtype.UUID{}, nil)
			}
		}
	}
	// Dependencies decide which branches are ready. Scheduling adds one more
	// invariant: a single agent owns at most one active work unit in this run.
	// Independent branches still fan out to different agents immediately.
	dispatchable := selectDispatchableOrchestrationSteps(steps, runnable, maxConcurrency)
	for _, step := range dispatchable {
		if !step.AgentID.Valid {
			return fmt.Errorf("step %s has no routed agent", step.StepKey)
		}
		if step.Stage != "plan" && artifactLocation != "" {
			if _, topologyErr := h.validateOrchestrationArtifactRoutes(ctx, issue, []pgtype.UUID{step.AgentID}, artifactLocation); topologyErr != nil {
				return topologyErr
			}
		}
		// Queueing, task insertion, and linkage are one transaction. An occupied
		// agent slot remains temporary backpressure; no attempt is charged until
		// a linked task generation commits.
		task, event, enqueueErr := h.queueOrchestrationStepAtomically(ctx, runID, issue, step, artifactLocation, maxConcurrency)
		if enqueueErr != nil {
			switch {
			case errors.Is(enqueueErr, errOrchestrationDispatchSuperseded), errors.Is(enqueueErr, errOrchestrationDispatchAtCapacity):
				continue
			case errors.Is(enqueueErr, errOrchestrationDispatchBusy), isUniqueViolation(enqueueErr):
				h.createOrchestrationEvent(ctx, runID, step.ID, "dispatch_deferred", "system", pgtype.UUID{}, map[string]any{"reason": "agent_busy"})
				continue
			case errors.Is(enqueueErr, errOrchestrationArtifactMoved):
				h.createOrchestrationEvent(ctx, runID, step.ID, "dispatch_deferred", "system", pgtype.UUID{}, map[string]any{
					"reason": "artifact_location_changed", "expected": artifactLocation,
				})
				return enqueueErr
			default:
				return enqueueErr
			}
		}
		h.TaskService.PublishTaskEnqueued(ctx, task)
		h.publishOrchestrationChanged(ctx, run, event)
	}
	_, _, err = h.reconcileOrchestrationRunLifecycle(ctx, runID, "running")
	return err
}

// selectDispatchableOrchestrationSteps is the pure scheduling rule behind the
// dispatcher. Dependencies are already resolved by ListRunnable...; this layer
// applies the run concurrency cap and per-agent serialization. Human approval
// rows are handled before this function and never consume an agent slot.
func selectDispatchableOrchestrationSteps(
	steps []db.OrchestrationStep,
	runnable []db.OrchestrationStep,
	maxConcurrency int,
) []db.OrchestrationStep {
	if maxConcurrency < 1 {
		return nil
	}
	active := 0
	busyAgents := make(map[string]struct{})
	for _, step := range steps {
		if step.Status != "queued" && step.Status != "running" {
			continue
		}
		active++
		if step.AgentID.Valid {
			busyAgents[uuidToString(step.AgentID)] = struct{}{}
		}
	}

	capacity := maxConcurrency - active
	if capacity < 0 {
		capacity = 0
	}
	selected := make([]db.OrchestrationStep, 0, min(len(runnable), capacity))
	for _, step := range runnable {
		if active >= maxConcurrency {
			break
		}
		if step.ApprovalRequired && !step.AgentID.Valid {
			continue
		}
		// Keep invalid routing visible to the caller, which returns a useful
		// error instead of silently stalling the run.
		if !step.AgentID.Valid {
			selected = append(selected, step)
			active++
			continue
		}
		agentID := uuidToString(step.AgentID)
		if _, busy := busyAgents[agentID]; busy {
			continue
		}
		selected = append(selected, step)
		busyAgents[agentID] = struct{}{}
		active++
	}
	return selected
}

// pauseManualOrchestrationBatch stops manual progression only after every work
// unit dispatched by the previous human Continue action has reached a terminal
// state. A sibling branch may still be running; in that case the run remains
// visibly active, but no newly-ready work is dispatched.
func (h *Handler) pauseManualOrchestrationBatch(ctx context.Context, runID, completedStepID pgtype.UUID) {
	run, err := h.Queries.GetOrchestrationRun(ctx, runID)
	if err != nil {
		return
	}
	if run, err = setManualOrchestrationDispatchAuthorization(ctx, h.Queries, run, false); err != nil {
		return
	}
	if run.Status == "waiting_approval" || orchestrationRunStatusIsTerminal(run.Status) {
		return
	}
	status, _, err := h.reconcileOrchestrationRunLifecycle(ctx, runID, "waiting_approval")
	if err != nil || status != "waiting_approval" {
		return
	}
	h.createOrchestrationEvent(ctx, runID, completedStepID, "progression_paused", "system", pgtype.UUID{}, map[string]any{"policy": "manual"})
}

func (h *Handler) handleOrchestrationTaskTerminal(ctx context.Context, task db.AgentTaskQueue) error {
	repairPendingStep := false
	step, err := h.Queries.GetOrchestrationStepByTask(ctx, task.ID)
	if err != nil && task.OrchestrationStepID.Valid {
		candidate, candidateErr := h.Queries.GetOrchestrationStep(ctx, task.OrchestrationStepID)
		if candidateErr != nil {
			return candidateErr
		}
		latestTask, latestErr := h.Queries.GetLatestTaskForOrchestrationStep(ctx, candidate.ID)
		if latestErr != nil {
			return latestErr
		}
		if latestTask.ID != task.ID {
			// A retry/new continuation now owns the step. A delayed terminal
			// callback from an older task must not overwrite that lineage.
			return nil
		}
		// Legacy servers charged the step attempt before inserting its next
		// task. If they crashed in that gap, the previous terminal task is still
		// "latest" but predates the queue transition and therefore belongs to an
		// older generation. Never reattach it. Roll the orphaned charge back to
		// pending so the atomic dispatcher can create the missing generation.
		olderGeneration := latestTask.CreatedAt.Valid && candidate.UpdatedAt.Valid &&
			latestTask.CreatedAt.Time.Before(candidate.UpdatedAt.Time)
		if !candidate.TaskID.Valid && candidate.Status == "queued" && olderGeneration {
			step, err = h.Queries.DeferOrchestrationStepDispatch(ctx, candidate.ID)
			if err != nil {
				return err
			}
			repairPendingStep = true
		} else {
			switch {
			case candidate.TaskID.Valid && candidate.TaskID != task.ID:
				return nil
			case !candidate.TaskID.Valid && (candidate.Status == "queued" || candidate.Status == "running"):
				step, err = h.Queries.AttachTaskToOrchestrationStep(ctx, db.AttachTaskToOrchestrationStepParams{ID: candidate.ID, TaskID: task.ID})
				if err != nil {
					return err
				}
			case !candidate.TaskID.Valid && candidate.Status == "pending":
				// The prior failure callback reset this step but died before dispatch.
				step, err = candidate, nil
				repairPendingStep = true
			default:
				step, err = candidate, nil
			}
		}
	}
	if err != nil {
		// An ordinary task may have occupied this issue+agent queue slot while a
		// run was active. Its terminal transition is the wake-up edge for any
		// orchestration step that was deliberately left pending behind it.
		if task.IssueID.Valid {
			if run, runErr := h.Queries.GetActiveOrchestrationRunForIssue(ctx, task.IssueID); runErr == nil && run.Status != "draft" {
				if issue, issueErr := h.Queries.GetIssue(ctx, task.IssueID); issueErr == nil {
					return h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
				}
			}
		}
		return nil
	}
	run, err := h.Queries.GetOrchestrationRun(ctx, step.RunID)
	if err != nil {
		return err
	}
	issue, err := h.Queries.GetIssue(ctx, run.IssueID)
	if err != nil {
		return err
	}
	if repairPendingStep {
		if run.ProgressionPolicy == "manual" {
			if run, err = setManualOrchestrationDispatchAuthorization(ctx, h.Queries, run, false); err != nil {
				return err
			}
			status, _, reconcileErr := h.reconcileOrchestrationRunLifecycle(ctx, run.ID, "waiting_approval")
			if reconcileErr != nil {
				return reconcileErr
			}
			if status == "waiting_approval" && run.Status != "waiting_approval" {
				h.createOrchestrationEvent(ctx, run.ID, step.ID, "progression_paused", "system", pgtype.UUID{}, map[string]any{"policy": "manual"})
			}
			return nil
		}
		return h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
	}
	if task.Status == "cancelled" {
		if step.Status != "cancelled" {
			step, err = h.Queries.CancelOrchestrationStepByTask(ctx, task.ID)
			if err != nil {
				return err
			}
			h.createOrchestrationEvent(ctx, run.ID, step.ID, "step_cancelled", "member", pgtype.UUID{}, nil)
		}
		return h.failOrchestrationRunUnlessTerminal(ctx, run.ID)
	}
	if task.Status == "completed" {
		// Completion callbacks are replayable. A server failure after this state
		// transition but before downstream dispatch causes the daemon to retry the
		// terminal request; resume from the persisted step instead of duplicating
		// its handoff message.
		if step.Status != "queued" && step.Status != "running" {
			switch step.Status {
			case "completed":
				if run.ProgressionPolicy == "manual" {
					h.pauseManualOrchestrationBatch(ctx, run.ID, step.ID)
					return nil
				}
				return h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
			case "waiting_approval", "waiting_input", "blocked", "cancelled", "skipped":
				return nil
			}
		}
		step, eventKind, err := h.persistCompletedOrchestrationHandoff(ctx, run, step, task)
		if err != nil {
			return err
		}
		if step.Status == "waiting_approval" {
			return nil
		}
		if eventKind == "input_requested" || eventKind == "step_blocked" {
			return nil
		}
		if run.ProgressionPolicy == "manual" {
			h.pauseManualOrchestrationBatch(ctx, run.ID, step.ID)
			return nil
		}
		return h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
	}
	if step.Status == "blocked" {
		return nil
	}
	if step.Status != "failed" {
		step, err = h.Queries.FailOrchestrationStep(ctx, db.FailOrchestrationStepParams{ID: task.ID, Error: task.Error})
		if err != nil {
			return err
		}
		h.createOrchestrationEvent(ctx, run.ID, step.ID, "step_failed", "agent", task.AgentID, map[string]any{"error": task.Error.String, "attempt": step.Attempt})
	}
	latestRun, err := h.Queries.GetOrchestrationRun(ctx, run.ID)
	if err != nil {
		return err
	}
	if orchestrationRunStatusIsTerminal(latestRun.Status) {
		return nil
	}
	if run, err = setManualOrchestrationDispatchAuthorization(ctx, h.Queries, run, false); err != nil {
		return err
	}
	if step.Attempt < step.MaxAttempts {
		if _, err = h.Queries.ResetOrchestrationStepForRetry(ctx, step.ID); err == nil {
			eventKind := "step_retrying"
			if run.ProgressionPolicy == "manual" {
				eventKind = "step_retry_ready"
			}
			h.createOrchestrationEvent(ctx, run.ID, step.ID, eventKind, "system", pgtype.UUID{}, map[string]any{"next_attempt": step.Attempt + 1})
			if run.ProgressionPolicy == "manual" {
				h.pauseManualOrchestrationBatch(ctx, run.ID, step.ID)
				return nil
			}
			return h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	_, _, err = h.reconcileOrchestrationRunLifecycle(ctx, run.ID, "running")
	return err
}

func enforceOrchestrationVerificationGate(stage string, handoff *service.OrchestrationHandoff) {
	// Independent gates fail closed. A provider exit code cannot unblock
	// release without an explicit pass verdict, and a claimed pass cannot
	// contradict failed verification evidence in the same handoff.
	if handoff.Outcome == "completed" && (stage == "qa" || stage == "review") {
		blocker := ""
		switch handoff.Verdict {
		case "pass":
			hasPassedVerification := false
			for _, verification := range handoff.Verification {
				if verification.Status == "failed" {
					handoff.Verdict = "fail"
					blocker = "A required verification check failed: " + verification.Name
					break
				}
				hasPassedVerification = hasPassedVerification || verification.Status == "passed"
			}
			if blocker == "" && stage == "qa" && !hasPassedVerification {
				blocker = "The QA stage claimed a pass without any passed verification evidence."
			}
		case "fail":
			blocker = handoff.Summary
		default:
			blocker = "The " + stage + " stage did not provide the required pass/fail verdict."
		}
		if blocker != "" {
			handoff.Outcome = "blocked"
			if len(handoff.Blockers) == 0 {
				handoff.Blockers = append(handoff.Blockers, blocker)
			}
		}
	}
}

func (h *Handler) persistCompletedOrchestrationHandoff(ctx context.Context, run db.OrchestrationRun, current db.OrchestrationStep, task db.AgentTaskQueue) (db.OrchestrationStep, string, error) {
	var completion struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(task.Result, &completion)
	handoff, parsed := service.NormalizeOrchestrationHandoff(current.Stage, completion.Output)
	enforceOrchestrationVerificationGate(current.Stage, &handoff)
	queries := h.Queries
	var tx pgx.Tx
	var err error
	terminalRunStatus := ""
	if h.TxStarter != nil {
		tx, err = h.TxStarter.Begin(ctx)
		if err != nil {
			return current, "", err
		}
		defer tx.Rollback(ctx)
		queries = h.Queries.WithTx(tx)
		lockedRun, lockErr := queries.LockOrchestrationRun(ctx, run.ID)
		if lockErr != nil {
			return current, "", lockErr
		}
		if orchestrationRunStatusIsTerminal(lockedRun.Status) {
			terminalRunStatus = lockedRun.Status
		}
		run = lockedRun
	} else {
		lockedRun, lockErr := queries.GetOrchestrationRun(ctx, run.ID)
		if lockErr != nil {
			return current, "", lockErr
		}
		if orchestrationRunStatusIsTerminal(lockedRun.Status) {
			terminalRunStatus = lockedRun.Status
		}
		run = lockedRun
	}
	if terminalRunStatus == "" && handoff.Outcome == "waiting_input" {
		questionCount, countErr := queries.CountOrchestrationStepQuestions(ctx, current.ID)
		if countErr != nil {
			return current, "", countErr
		}
		if questionCount >= maxOrchestrationClarificationRounds {
			handoff.Outcome = "blocked"
			handoff.Question = nil
			handoff.Blockers = append(handoff.Blockers,
				fmt.Sprintf("Clarification limit reached after %d answered rounds; the work unit needs a human scope correction before retry.", maxOrchestrationClarificationRounds),
			)
		}
	}
	payload, err := json.Marshal(handoff)
	if err != nil {
		return current, "", err
	}
	if terminalRunStatus == "" {
		run, err = setManualOrchestrationDispatchAuthorization(ctx, queries, run, false)
		if err != nil {
			return current, "", err
		}
	}

	eventKind := "step_completed"
	messageKind := "handoff"
	targetType := "run"
	var targetID pgtype.UUID
	expectsReply := false
	if terminalRunStatus != "" {
		finalStatus := "blocked"
		var terminalError pgtype.Text
		if handoff.Outcome == "completed" {
			finalStatus = "completed"
		} else {
			terminalError = pgtype.Text{
				String: "result arrived after orchestration run reached " + terminalRunStatus,
				Valid:  true,
			}
		}
		current, err = queries.FinalizeOrchestrationStepAfterTerminalRun(ctx, db.FinalizeOrchestrationStepAfterTerminalRunParams{
			TaskID: task.ID,
			Status: finalStatus,
			Output: payload,
			Error:  terminalError,
		})
		eventKind = "step_late_result_recorded"
	} else {
		switch handoff.Outcome {
		case "waiting_input":
			current, err = queries.WaitOrchestrationStepInput(ctx, db.WaitOrchestrationStepInputParams{ID: task.ID, Output: payload})
			eventKind, messageKind, expectsReply = "input_requested", "question", true
			targetType = handoff.Question.Target
			switch targetType {
			case "controller":
				targetID = current.ControllerAgentID
			case "agent":
				if parsedTarget, parseErr := parseOptionalUUID(handoff.Question.TargetID); parseErr == nil && parsedTarget.Valid {
					targetID = parsedTarget
				} else {
					targetID = current.AgentID
				}
			case "human":
			default:
				targetType = "human"
			}
		case "blocked":
			current, err = queries.BlockOrchestrationStep(ctx, db.BlockOrchestrationStepParams{ID: task.ID, Output: payload})
			eventKind, messageKind, targetType = "step_blocked", "blocker", "human"
		default:
			current, err = queries.CompleteOrchestrationStep(ctx, db.CompleteOrchestrationStepParams{ID: task.ID, Output: payload})
			if err == nil && current.Status == "waiting_approval" {
				eventKind = "approval_requested"
			}
		}
	}
	if err != nil {
		return current, "", err
	}

	correlationID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	message, err := queries.CreateOrchestrationMessage(ctx, db.CreateOrchestrationMessageParams{
		RunID: run.ID, StepID: current.ID, Kind: messageKind, ActorType: "agent", ActorID: task.AgentID,
		TargetType: targetType, TargetID: targetID, Body: payload, PlanVersion: run.PlanVersion,
		CorrelationID: correlationID, IdempotencyKey: fmt.Sprintf("task:%s:%s", uuidToString(task.ID), messageKind),
		ExpectsReply: expectsReply,
	})
	if err != nil {
		return current, "", err
	}
	details, _ := json.Marshal(map[string]any{
		"message_id": uuidToString(message.ID), "outcome": handoff.Outcome,
		"verdict": handoff.Verdict, "structured": parsed, "legacy": handoff.Legacy,
	})
	event, err := queries.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		RunID: run.ID, StepID: current.ID, Kind: eventKind, ActorType: "agent", ActorID: task.AgentID, Details: details,
	})
	if err != nil {
		return current, "", err
	}
	var lifecycleEvents []db.OrchestrationEvent
	switch current.Status {
	case "waiting_approval", "waiting_input", "blocked":
		if _, _, err = setOrchestrationRunLifecycleStatus(ctx, queries, run.ID, "running"); err != nil {
			return current, "", err
		}
	case "completed":
		if run.ProgressionPolicy == "manual" && terminalRunStatus == "" {
			status, terminal, statusErr := setOrchestrationRunLifecycleStatus(ctx, queries, run.ID, "waiting_approval")
			if statusErr != nil {
				return current, "", statusErr
			}
			kind := ""
			details := []byte(`{}`)
			if terminal && status != run.Status {
				kind = "run_" + status
			} else if status == "waiting_approval" && run.Status != "waiting_approval" {
				kind = "progression_paused"
				details = []byte(`{"policy":"manual"}`)
			}
			if kind != "" {
				lifecycleEvent, lifecycleErr := queries.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
					RunID: run.ID, StepID: current.ID, Kind: kind, ActorType: "system", Details: details,
				})
				if lifecycleErr != nil {
					return current, "", lifecycleErr
				}
				lifecycleEvents = append(lifecycleEvents, lifecycleEvent)
			}
		}
	}
	if tx != nil {
		if err = tx.Commit(ctx); err != nil {
			return current, "", err
		}
	}
	h.publishOrchestrationChanged(ctx, run, event)
	for _, lifecycleEvent := range lifecycleEvents {
		h.publishOrchestrationChanged(ctx, run, lifecycleEvent)
	}
	return current, eventKind, nil
}

func (h *Handler) createOrchestrationEvent(ctx context.Context, runID, stepID pgtype.UUID, kind, actorType string, actorID pgtype.UUID, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	payload, _ := json.Marshal(details)
	event, err := h.Queries.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{RunID: runID, StepID: stepID, Kind: kind, ActorType: actorType, ActorID: actorID, Details: payload})
	if err != nil {
		slog.Warn("create orchestration event failed", "run_id", uuidToString(runID), "kind", kind, "error", err)
		return
	}
	run, err := h.Queries.GetOrchestrationRun(ctx, runID)
	if err != nil {
		slog.Warn("publish orchestration event: load run failed", "run_id", uuidToString(runID), "kind", kind, "error", err)
		return
	}
	h.publishOrchestrationChanged(ctx, run, event)
}

func (h *Handler) publishOrchestrationChanged(ctx context.Context, run db.OrchestrationRun, event db.OrchestrationEvent) {
	issue, err := h.Queries.GetIssue(ctx, run.IssueID)
	if err != nil {
		slog.Warn("publish orchestration event: load issue failed", "run_id", uuidToString(run.ID), "event_id", uuidToString(event.ID), "error", err)
		return
	}
	h.publish(protocol.EventOrchestrationChanged, uuidToString(issue.WorkspaceID), event.ActorType, uuidToString(event.ActorID), protocol.OrchestrationChangedPayload{
		IssueID: uuidToString(run.IssueID), RunID: uuidToString(run.ID), StepID: uuidToString(event.StepID),
		Kind: event.Kind, EventID: uuidToString(event.ID), PlanVersion: run.PlanVersion,
	})
}
