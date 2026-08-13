package handler

import (
	"strings"
	"unicode/utf8"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

const taskExecutionLevelPolicyVersion = 1

type taskExecutionLevelPolicy struct {
	Requested     string   `json:"requested"`
	Resolved      string   `json:"resolved"`
	PolicyVersion int      `json:"policy_version"`
	Score         int      `json:"score"`
	Signals       []string `json:"signals"`
}

type taskExecutionLevelDefaults struct {
	ProgressionPolicy string
	ModelRoutingMode  string
	MaxConcurrency    int
	PlanShape         string
	ReviewPlanFirst   bool
}

func normalizeTaskExecutionLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto":
		return "auto"
	case "assist":
		return "assist"
	case "direct":
		return "direct"
	case "standard":
		return "standard"
	case "coordinated":
		return "coordinated"
	case "controlled":
		return "controlled"
	default:
		return ""
	}
}

func taskExecutionLevelRank(level string) int {
	switch level {
	case "assist":
		return 0
	case "direct":
		return 1
	case "standard":
		return 2
	case "coordinated":
		return 3
	case "controlled":
		return 4
	default:
		return -1
	}
}

func higherTaskExecutionLevel(left, right string) string {
	if taskExecutionLevelRank(right) > taskExecutionLevelRank(left) {
		return right
	}
	return left
}

func taskExecutionLevelIssueText(issue db.Issue) string {
	parts := []string{issue.Title}
	if issue.Description.Valid {
		parts = append(parts, issue.Description.String)
	}
	if len(issue.AcceptanceCriteria) > 0 {
		parts = append(parts, string(issue.AcceptanceCriteria))
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

func containsTaskLevelMarker(body string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func inferTaskExecutionLevel(issue db.Issue) (recommended string, safetyFloor string, score int, signals []string) {
	body := taskExecutionLevelIssueText(issue)
	score = 2 // Standard preserves Agora's existing verified execution path.
	safetyFloor = "assist"
	seen := make(map[string]bool)
	add := func(signal string, points int, floor string) {
		if seen[signal] {
			return
		}
		seen[signal] = true
		signals = append(signals, signal)
		score += points
		safetyFloor = higherTaskExecutionLevel(safetyFloor, floor)
	}

	if utf8.RuneCountInString(body) > 1200 {
		add("large_scope", 1, "standard")
	}
	if containsTaskLevelMarker(body,
		"cross-repo", "cross repo", "multiple repositories", "multiple repos",
	) {
		add("cross_repository", 3, "coordinated")
	}
	if containsTaskLevelMarker(body,
		"frontend and backend", "backend and frontend", "ui and api", "api and ui", "full-stack", "full stack",
	) {
		add("frontend_and_backend", 2, "standard")
	}
	if containsTaskLevelMarker(body,
		"desktop and web", "web and desktop", "mobile and web", "web and mobile", "ios and android", "android and ios",
	) {
		add("multiple_surfaces", 3, "coordinated")
	}
	if containsTaskLevelMarker(body,
		"schema migration", "data migration", "database migration", "backfill", "migrate existing data",
	) {
		add("data_or_schema_migration", 3, "coordinated")
	}
	if containsTaskLevelMarker(body,
		"file upload", "image upload", "attachment upload", "signed url", "private file",
	) {
		add("governed_file_handling", 1, "standard")
	}
	if containsTaskLevelMarker(body,
		"oauth", "authentication", "authorization", "access control", "permission model", "role based access", "rbac",
	) {
		add("security_or_permissions", 3, "coordinated")
	}
	if containsTaskLevelMarker(body,
		"production deploy", "deploy to production", "production release", "production database", "rotate secret",
		"rotate credential", "api key rotation", "delete all", "drop table", "irreversible",
	) {
		add("privileged_or_destructive_operation", 6, "controlled")
	}
	if containsTaskLevelMarker(body,
		"billing", "payment", "invoice charge", "refund", "payout", "financial transaction",
	) {
		add("billing_or_financial_change", 6, "controlled")
	}

	switch {
	case safetyFloor == "controlled":
		recommended = "controlled"
	case score >= 4:
		recommended = "coordinated"
	default:
		recommended = "standard"
	}
	return recommended, safetyFloor, score, signals
}

func resolveTaskExecutionLevel(issue db.Issue, requested string) taskExecutionLevelPolicy {
	requested = normalizeTaskExecutionLevel(requested)
	if requested == "" {
		requested = "auto"
	}
	recommended, safetyFloor, score, signals := inferTaskExecutionLevel(issue)
	resolved := requested
	if requested == "auto" {
		resolved = recommended
	}
	if elevated := higherTaskExecutionLevel(resolved, safetyFloor); elevated != resolved {
		resolved = elevated
		signals = append(signals, "safety_floor_applied")
	}
	return taskExecutionLevelPolicy{
		Requested: requested, Resolved: resolved, PolicyVersion: taskExecutionLevelPolicyVersion,
		Score: score, Signals: signals,
	}
}

func defaultsForTaskExecutionLevel(level string) taskExecutionLevelDefaults {
	switch level {
	case "assist":
		return taskExecutionLevelDefaults{ProgressionPolicy: "automatic", ModelRoutingMode: "balanced", MaxConcurrency: 1, PlanShape: squadPlanShapeLean}
	case "direct":
		return taskExecutionLevelDefaults{ProgressionPolicy: "automatic", ModelRoutingMode: "cost", MaxConcurrency: 1, PlanShape: squadPlanShapeLean}
	case "coordinated":
		return taskExecutionLevelDefaults{ProgressionPolicy: "gated", ModelRoutingMode: "intelligence", MaxConcurrency: 4, PlanShape: squadPlanShapeFull}
	case "controlled":
		return taskExecutionLevelDefaults{ProgressionPolicy: "gated", ModelRoutingMode: "intelligence", MaxConcurrency: 2, PlanShape: squadPlanShapeFull, ReviewPlanFirst: true}
	default:
		return taskExecutionLevelDefaults{ProgressionPolicy: "automatic", ModelRoutingMode: "balanced", MaxConcurrency: 3}
	}
}

func progressionForTaskExecutionLevel(level, progression string) string {
	if level == "controlled" {
		return "gated"
	}
	return progression
}

func autoStartForTaskExecutionLevel(level string, requested, projectReviewPlanFirst *bool) bool {
	autoStart := true
	if requested != nil {
		autoStart = *requested
	} else if projectReviewPlanFirst != nil {
		autoStart = !*projectReviewPlanFirst
	}
	if defaultsForTaskExecutionLevel(level).ReviewPlanFirst {
		return false
	}
	return autoStart
}

func planShapeForTaskExecutionLevel(issue db.Issue, level string) string {
	if configured := defaultsForTaskExecutionLevel(level).PlanShape; configured != "" {
		return configured
	}
	return inferSquadPlanShape(issue)
}

func assistOrchestrationSteps(routing orchestrationRouting, strategy string) []orchestrationStepRequest {
	if strategy == "human" {
		return []orchestrationStepRequest{{
			Key: "human-plan", Title: "Prepare the plan", Stage: "plan", Capability: "coordination",
			ApprovalRequired: true, HumanOnly: true, MaxAttempts: 1,
		}}
	}
	agentID := routing.ControllerAgent
	if strategy == "solo" || !agentID.Valid {
		agentID = routing.DevelopmentAgent
	}
	return []orchestrationStepRequest{{
		Key: "plan", Title: "Analyze the issue and prepare a plan", Stage: "plan", Capability: "coordination",
		AgentID: uuidToString(agentID), MaxAttempts: 2,
		Instructions: "Inspect the issue and its relevant resources. Produce an implementation-ready plan, risks, and verification approach without editing implementation files.",
	}}
}
