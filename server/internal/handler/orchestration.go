package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

type orchestrationPlanRequest struct {
	// Mode is the deprecated auto/manual alias. New callers send the two
	// orthogonal fields below: who executes and how ready work advances.
	Mode              string                     `json:"mode"`
	ExecutionStrategy string                     `json:"execution_strategy"`
	ProgressionPolicy string                     `json:"progression_policy"`
	SquadID           string                     `json:"squad_id"`
	AutoStart         bool                       `json:"auto_start"`
	Policy            map[string]any             `json:"policy"`
	Steps             []orchestrationStepRequest `json:"steps"`
}

type orchestrationStepRequest struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	Stage            string   `json:"stage"`
	AgentID          string   `json:"agent_id"`
	Model            string   `json:"model"`
	Instructions     string   `json:"instructions"`
	ApprovalRequired bool     `json:"approval_required"`
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
	OwnerType        string
	OwnerID          pgtype.UUID
	ControllerAgent  pgtype.UUID
	DevelopmentAgent pgtype.UUID
	ExecutionMode    string
}

type orchestrationPlannerMember struct {
	AgentID      pgtype.UUID
	Name         string
	Role         string
	Description  string
	Instructions string
	IsLeader     bool
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
	TaskID             string          `json:"task_id,omitempty"`
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
	return run.Status == "running" || run.Status == "waiting_approval"
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
	}
	return routing
}

func inferExecutionStrategy(routing orchestrationRouting, customPlan bool) string {
	if customPlan {
		return "custom"
	}
	if routing.OwnerType == "member" || routing.OwnerType == "unassigned" {
		return "human"
	}
	if routing.OwnerType == "squad" || routing.ExecutionMode != "direct" {
		return "squad"
	}
	return "solo"
}

func progressionPolicyForIssue(issue db.Issue, requested, legacyMode string) string {
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
	return "automatic"
}

func defaultOrchestrationSteps(issue db.Issue, routing orchestrationRouting, strategy string) []orchestrationStepRequest {
	return defaultOrchestrationStepsWithMembers(issue, routing, strategy, nil)
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

func defaultOrchestrationStepsWithMembers(issue db.Issue, routing orchestrationRouting, strategy string, members []orchestrationPlannerMember) []orchestrationStepRequest {
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

	steps := []orchestrationStepRequest{{
		Key: "plan", Title: "Plan the work", Stage: "plan", Capability: "coordination", AgentID: uuidToString(controller),
		MaxAttempts: 2, SquadID: squadID, Instructions: controllerPlanInstructions,
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
			Instructions: instructions, MaxAttempts: 2, DependsOnKeys: []string{"plan"}, ParentKey: "plan", SquadID: squadID,
		})
		developmentKeys = append(developmentKeys, key)
	}
	integrationAgent := controller
	if !integrationAgent.Valid {
		integrationAgent = development
	}
	steps = append(steps,
		orchestrationStepRequest{Key: "integrate", Title: "Integrate implementation branches", Stage: "dev", Kind: "integration", Capability: "integration", AgentID: uuidToString(integrationAgent), MaxAttempts: 2, DependsOnKeys: developmentKeys, SquadID: squadID},
		orchestrationStepRequest{Key: "qa", Title: "Verify the integrated result", Stage: "qa", Capability: "qa", AgentID: uuidToString(qaAgent), MaxAttempts: 2, DependsOnKeys: []string{"integrate"}, SquadID: squadID},
		orchestrationStepRequest{Key: "review", Title: "Review the integrated result", Stage: "review", Capability: "review", AgentID: uuidToString(reviewAgent), MaxAttempts: 2, DependsOnKeys: []string{"integrate"}, SquadID: squadID},
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
			ApprovalRequired: true, MaxAttempts: 2, DependsOnKeys: []string{"qa", "review"}, SquadID: squadID,
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
	var req orchestrationPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ProgressionPolicy) != "" && normalizeProgressionPolicy(req.ProgressionPolicy) == "" {
		writeError(w, http.StatusBadRequest, "progression_policy must be automatic, gated, or manual")
		return
	}
	if strings.TrimSpace(req.Mode) != "" && !strings.EqualFold(req.Mode, "auto") && !strings.EqualFold(req.Mode, "manual") {
		writeError(w, http.StatusBadRequest, "mode must be auto or manual")
		return
	}
	progressionPolicy := progressionPolicyForIssue(issue, req.ProgressionPolicy, req.Mode)
	if progressionPolicy == "" {
		writeError(w, http.StatusBadRequest, "progression_policy must be automatic, gated, or manual")
		return
	}
	routing := h.orchestrationRouting(r.Context(), issue)
	hasCustomPlan := len(req.Steps) > 0
	executionStrategy := strings.ToLower(strings.TrimSpace(req.ExecutionStrategy))
	if executionStrategy == "" {
		executionStrategy = inferExecutionStrategy(routing, hasCustomPlan)
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
	if executionStrategy == "squad" && routing.OwnerType != "squad" {
		writeError(w, http.StatusBadRequest, "squad_id is required when the issue is not assigned to a squad")
		return
	}
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
	}
	if !hasCustomPlan {
		req.Steps = defaultOrchestrationStepsWithMembers(issue, routing, executionStrategy, h.orchestrationPlannerMembers(r.Context(), issue, routing, executionStrategy))
	}
	if len(req.Steps) > 20 {
		writeError(w, http.StatusBadRequest, "plan cannot exceed 20 steps")
		return
	}
	if err := prepareOrchestrationPlan(req.Steps); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	if routing.OwnerType == "squad" {
		req.Policy["squad_id"] = uuidToString(routing.OwnerID)
	}
	// Compatibility field for old clients; never read as the new source of
	// truth after the run row is created.
	req.Policy["execution_mode"] = legacyExecutionMode(executionStrategy)
	if _, configured := req.Policy["max_concurrency"]; !configured {
		req.Policy["max_concurrency"] = 3
	}
	policy, _ := json.Marshal(req.Policy)
	takenOverTasks := 0
	if req.AutoStart {
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
	run, err := h.Queries.CreateOrchestrationRun(r.Context(), db.CreateOrchestrationRunParams{
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
			h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
			writeError(w, http.StatusBadRequest, "every step requires a unique key, title, and valid stage")
			return
		}
		if input.Kind != "task" && input.Kind != "integration" {
			h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
			writeError(w, http.StatusBadRequest, "step kind must be task or integration")
			return
		}
		agentID, parseErr := parseOptionalUUID(input.AgentID)
		if parseErr != nil {
			h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
			writeError(w, http.StatusBadRequest, "invalid step agent_id")
			return
		}
		var squadID, controllerAgentID pgtype.UUID
		if strings.TrimSpace(input.SquadID) != "" {
			squadID, controllerAgentID, parseErr = h.resolveSquadStep(r.Context(), issue.WorkspaceID, input)
			if parseErr != nil {
				h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
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
					h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
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
		if input.ApprovalRequired && !agentID.Valid && !controllerAgentID.Valid {
			controllerAgentID = routing.ControllerAgent
			if !controllerAgentID.Valid {
				controllerAgentID = routing.DevelopmentAgent
			}
		}
		if !agentID.Valid && !input.ApprovalRequired {
			h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
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
				h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
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
				h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
				writeError(w, http.StatusBadRequest, "parent_key must reference an earlier step")
				return
			}
			parentStepID = parent.ID
		}
		step, createErr := h.Queries.CreateOrchestrationStep(r.Context(), db.CreateOrchestrationStepParams{
			RunID: run.ID, StepKey: input.Key, Title: input.Title, Stage: input.Stage,
			Position: int32(index), AgentID: agentID,
			ModelOverride:   pgtype.Text{String: strings.TrimSpace(input.Model), Valid: strings.TrimSpace(input.Model) != ""},
			DependsOnStepID: legacyDependency, ApprovalRequired: input.ApprovalRequired,
			MaxAttempts: input.MaxAttempts, Instructions: strings.TrimSpace(input.Instructions),
			ParentStepID: parentStepID, SquadID: squadID, ControllerAgentID: controllerAgentID,
			IntroducedInVersion: 1, StepKind: input.Kind, Capability: input.Capability,
		})
		if createErr != nil {
			h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
			writeError(w, http.StatusBadRequest, "step keys must be unique")
			return
		}
		stepsByKey[input.Key] = step
		for _, key := range input.DependsOnKeys {
			dependency := stepsByKey[strings.TrimSpace(key)]
			if depErr := h.Queries.AddOrchestrationStepDependency(r.Context(), db.AddOrchestrationStepDependencyParams{StepID: step.ID, DependsOnStepID: dependency.ID}); depErr != nil {
				h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "cancelled"})
				writeError(w, http.StatusBadRequest, "invalid step dependency")
				return
			}
		}
	}
	planEvent := "plan_proposed"
	if req.AutoStart {
		planEvent = "plan_created"
	}
	h.createOrchestrationEvent(r.Context(), run.ID, pgtype.UUID{}, planEvent, "member", parseUUID(userID), map[string]any{"steps": len(req.Steps), "taken_over_tasks": takenOverTasks})
	if req.AutoStart {
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
	revisions, _ := h.Queries.ListOrchestrationPlanRevisions(r.Context(), run.ID)
	response := orchestrationRunResponse{
		ID: uuidToString(run.ID), IssueID: uuidToString(run.IssueID), Status: run.Status, Mode: run.Mode,
		ExecutionStrategy: run.ExecutionStrategy, ProgressionPolicy: run.ProgressionPolicy,
		Policy: json.RawMessage(run.Policy), CreatedAt: run.CreatedAt.Time, UpdatedAt: run.UpdatedAt.Time,
		PlanVersion: run.PlanVersion, Steps: make([]orchestrationStepResponse, 0, len(steps)), Events: make([]orchestrationEventResponse, 0, len(events)), Revisions: make([]orchestrationRevisionResponse, 0, len(revisions)),
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
	for _, step := range steps {
		item := orchestrationStepResponse{
			ID: uuidToString(step.ID), Key: step.StepKey, Title: step.Title, Stage: step.Stage,
			Status: step.Status, Position: step.Position, AgentID: uuidToString(step.AgentID), Model: step.ModelOverride.String,
			TaskID: uuidToString(step.TaskID), ApprovalRequired: step.ApprovalRequired,
			ApprovedBy: uuidToString(step.ApprovedBy), Attempt: step.Attempt, MaxAttempts: step.MaxAttempts,
			Instructions: step.Instructions, Error: step.Error.String, DependsOnStepIDs: []string{},
			ParentStepID: uuidToString(step.ParentStepID), SquadID: uuidToString(step.SquadID), ControllerAgentID: uuidToString(step.ControllerAgentID),
			WorktreeBranch: step.WorktreeBranch.String, BaseSHA: step.BaseSha.String, HeadSHA: step.HeadSha.String, MergeStatus: step.MergeStatus, ConflictFiles: step.ConflictFiles,
			Kind: step.StepKind, Capability: step.Capability, IntegrationStatus: step.IntegrationStatus, IntegratedHeadSHAs: step.IntegratedHeadShas, MissingHeadSHAs: step.MissingHeadShas,
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
	var extraDependencies []pgtype.UUID
	var integrationJoinID pgtype.UUID
	var insertPosition int32
	if req.Operation == "reroute" {
		agentID, err = parseUUIDValue(req.AgentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "valid agent_id is required")
			return
		}
		if routeErr := h.validateOrchestrationAgentRoute(
			r.Context(), issue.WorkspaceID, run.ControllerAgentID, step.SquadID,
			step.ControllerAgentID, agentID, step.Capability,
		); routeErr != nil {
			writeError(w, http.StatusBadRequest, routeErr.Error())
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
		_, err = qtx.ReroutePendingOrchestrationStep(r.Context(), db.ReroutePendingOrchestrationStepParams{ID: step.ID, AgentID: agentID, ModelOverride: pgtype.Text{String: strings.TrimSpace(req.Model), Valid: strings.TrimSpace(req.Model) != ""}, Instructions: req.Instructions})
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
			created, createErr := qtx.CreateOrchestrationStep(r.Context(), db.CreateOrchestrationStepParams{
				RunID: run.ID, StepKey: strings.TrimSpace(child.Key), Title: strings.TrimSpace(child.Title), Stage: child.Stage,
				Position: insertPosition, AgentID: agentID, ModelOverride: pgtype.Text{String: strings.TrimSpace(child.Model), Valid: strings.TrimSpace(child.Model) != ""},
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
	run, err := h.Queries.GetActiveOrchestrationRunForIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no active orchestration")
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
	if step.Stage == "release" && !h.issueHasLabel(ctx, issue, sprintPRMergeOverrideLabel) {
		readiness := h.computeMergeReadiness(ctx, issue)
		if !readiness.Ready {
			reason := strings.Join(readiness.Blocked, "; ")
			if reason == "" {
				reason = "one or more required gates have not passed"
			}
			return step, fmt.Errorf("merge_gates_not_satisfied: %s", reason)
		}
	}
	approved, err := h.Queries.ApproveOrchestrationStep(ctx, db.ApproveOrchestrationStepParams{ID: step.ID, ApprovedBy: approvedBy})
	if err != nil {
		return step, fmt.Errorf("step is not waiting for approval")
	}
	h.createOrchestrationEvent(ctx, run.ID, approved.ID, "step_approved", "member", approvedBy, nil)
	_, _ = h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "running"})
	if dispatchErr := h.dispatchNextOrchestrationStep(ctx, run.ID, issue); dispatchErr != nil {
		slog.Warn("approved orchestration step dispatch failed", "run_id", uuidToString(run.ID), "step_id", uuidToString(approved.ID), "error", dispatchErr)
	}
	return approved, nil
}

func (h *Handler) RetryOrchestrationStep(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
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
	step, err = h.Queries.ResetOrchestrationStepForRetry(r.Context(), stepID)
	if err != nil {
		writeError(w, http.StatusConflict, "step has no retries remaining")
		return
	}
	h.Queries.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "running"})
	h.createOrchestrationEvent(r.Context(), run.ID, step.ID, "step_retry_requested", "member", parseUUID(requestUserID(r)), nil)
	_ = h.dispatchNextOrchestrationStep(r.Context(), run.ID, issue)
	h.writeIssueOrchestration(w, r, issue.ID, http.StatusAccepted)
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

// orchestrationTerminalRunStatus distinguishes a successful run from a branch
// cancellation that removed its mandatory release path. Cancelled optional
// work may still converge into a completed release, but a cancelled release
// (or any cancellation in a custom plan without a release gate) cannot be
// presented as successful completion.
func orchestrationTerminalRunStatus(steps []db.OrchestrationStep) (string, bool) {
	if len(steps) == 0 {
		return "", false
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

func (h *Handler) dispatchNextOrchestrationStep(ctx context.Context, runID pgtype.UUID, issue db.Issue) error {
	run, err := h.Queries.GetOrchestrationRun(ctx, runID)
	if err != nil {
		return err
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
		// The database enforces one active issue task per agent across both
		// orchestration and ordinary comment/assignment tasks. Treat an occupied
		// slot as temporary backpressure, not as a failed DAG branch. The ordinary
		// task terminal callback re-enters this dispatcher when the slot clears.
		if busy, busyErr := h.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
			IssueID: issue.ID,
			AgentID: step.AgentID,
		}); busyErr != nil {
			return busyErr
		} else if busy {
			continue
		}
		if _, queueErr := h.Queries.QueueOrchestrationStep(ctx, step.ID); queueErr != nil {
			if errors.Is(queueErr, pgx.ErrNoRows) {
				continue
			}
			return queueErr
		}
		task, enqueueErr := h.TaskService.EnqueueOrchestrationTask(ctx, issue, step.AgentID, step.ID, step.ModelOverride)
		if enqueueErr != nil {
			// Close the check/insert race with ordinary task enqueue paths. Do not
			// consume an attempt: this step never reached an agent.
			if isUniqueViolation(enqueueErr) {
				if _, deferErr := h.Queries.DeferOrchestrationStepDispatch(ctx, step.ID); deferErr != nil {
					return fmt.Errorf("defer busy orchestration step: %w", deferErr)
				}
				h.createOrchestrationEvent(ctx, runID, step.ID, "dispatch_deferred", "system", pgtype.UUID{}, map[string]any{"reason": "agent_busy"})
				continue
			}
			h.Queries.FailOrchestrationStepByID(ctx, db.FailOrchestrationStepByIDParams{ID: step.ID, Error: pgtype.Text{String: enqueueErr.Error(), Valid: true}})
			h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: "failed"})
			h.createOrchestrationEvent(ctx, runID, step.ID, "dispatch_failed", "system", pgtype.UUID{}, map[string]any{"error": enqueueErr.Error()})
			return enqueueErr
		}
		if _, attachErr := h.Queries.AttachTaskToOrchestrationStep(ctx, db.AttachTaskToOrchestrationStepParams{ID: step.ID, TaskID: task.ID}); attachErr != nil {
			return attachErr
		}
		h.createOrchestrationEvent(ctx, runID, step.ID, "step_queued", "agent", step.AgentID, map[string]any{"task_id": uuidToString(task.ID), "model": step.ModelOverride.String})
	}
	steps, err = h.Queries.ListOrchestrationSteps(ctx, runID)
	if err != nil {
		return err
	}
	waiting, hasActive := false, false
	for _, step := range steps {
		waiting = waiting || step.Status == "waiting_approval"
		hasActive = hasActive || step.Status == "queued" || step.Status == "running"
	}
	if terminalStatus, terminal := orchestrationTerminalRunStatus(steps); terminal {
		h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: terminalStatus})
		h.createOrchestrationEvent(ctx, runID, pgtype.UUID{}, "run_"+terminalStatus, "system", pgtype.UUID{}, nil)
	} else if waiting && !hasActive {
		h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: "waiting_approval"})
	} else {
		h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: "running"})
	}
	return nil
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
	steps, err := h.Queries.ListOrchestrationSteps(ctx, runID)
	if err != nil {
		return
	}
	for _, step := range steps {
		if step.Status == "queued" || step.Status == "running" {
			return
		}
	}
	if terminalStatus, terminal := orchestrationTerminalRunStatus(steps); terminal {
		if _, err := h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: terminalStatus}); err == nil {
			h.createOrchestrationEvent(ctx, runID, pgtype.UUID{}, "run_"+terminalStatus, "system", pgtype.UUID{}, nil)
		}
		return
	}
	if _, err := h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: runID, Status: "waiting_approval"}); err != nil {
		return
	}
	h.createOrchestrationEvent(ctx, runID, completedStepID, "progression_paused", "system", pgtype.UUID{}, map[string]any{"policy": "manual"})
}

func (h *Handler) handleOrchestrationTaskTerminal(ctx context.Context, task db.AgentTaskQueue) {
	step, err := h.Queries.GetOrchestrationStepByTask(ctx, task.ID)
	if err != nil {
		// An ordinary task may have occupied this issue+agent queue slot while a
		// run was active. Its terminal transition is the wake-up edge for any
		// orchestration step that was deliberately left pending behind it.
		if task.IssueID.Valid {
			if run, runErr := h.Queries.GetActiveOrchestrationRunForIssue(ctx, task.IssueID); runErr == nil && run.Status != "draft" {
				if issue, issueErr := h.Queries.GetIssue(ctx, task.IssueID); issueErr == nil {
					_ = h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
				}
			}
		}
		return
	}
	run, err := h.Queries.GetOrchestrationRun(ctx, step.RunID)
	if err != nil {
		return
	}
	issue, err := h.Queries.GetIssue(ctx, run.IssueID)
	if err != nil {
		return
	}
	if task.Status == "cancelled" {
		step, err = h.Queries.CancelOrchestrationStepByTask(ctx, task.ID)
		if err != nil {
			return
		}
		h.createOrchestrationEvent(ctx, run.ID, step.ID, "step_cancelled", "member", pgtype.UUID{}, nil)
		h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "failed"})
		return
	}
	if task.Status == "completed" {
		step, err = h.Queries.CompleteOrchestrationStep(ctx, db.CompleteOrchestrationStepParams{ID: task.ID, Output: task.Result})
		if err != nil {
			return
		}
		kind := "step_completed"
		if step.Status == "waiting_approval" {
			kind = "approval_requested"
		}
		h.createOrchestrationEvent(ctx, run.ID, step.ID, kind, "agent", task.AgentID, nil)
		if step.Status == "waiting_approval" {
			h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "waiting_approval"})
			return
		}
		if run.ProgressionPolicy == "manual" {
			h.pauseManualOrchestrationBatch(ctx, run.ID, step.ID)
			return
		}
		_ = h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
		return
	}
	step, err = h.Queries.FailOrchestrationStep(ctx, db.FailOrchestrationStepParams{ID: task.ID, Error: task.Error})
	if err != nil {
		return
	}
	h.createOrchestrationEvent(ctx, run.ID, step.ID, "step_failed", "agent", task.AgentID, map[string]any{"error": task.Error.String, "attempt": step.Attempt})
	if step.Attempt < step.MaxAttempts {
		if _, err := h.Queries.ResetOrchestrationStepForRetry(ctx, step.ID); err == nil {
			eventKind := "step_retrying"
			if run.ProgressionPolicy == "manual" {
				eventKind = "step_retry_ready"
			}
			h.createOrchestrationEvent(ctx, run.ID, step.ID, eventKind, "system", pgtype.UUID{}, map[string]any{"next_attempt": step.Attempt + 1})
			if run.ProgressionPolicy == "manual" {
				h.pauseManualOrchestrationBatch(ctx, run.ID, step.ID)
				return
			}
			_ = h.dispatchNextOrchestrationStep(ctx, run.ID, issue)
			return
		}
	}
	h.Queries.SetOrchestrationRunStatus(ctx, db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "failed"})
}

func (h *Handler) createOrchestrationEvent(ctx context.Context, runID, stepID pgtype.UUID, kind, actorType string, actorID pgtype.UUID, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	payload, _ := json.Marshal(details)
	if _, err := h.Queries.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{RunID: runID, StepID: stepID, Kind: kind, ActorType: actorType, ActorID: actorID, Details: payload}); err != nil {
		slog.Warn("create orchestration event failed", "run_id", uuidToString(runID), "kind", kind, "error", err)
	}
}
