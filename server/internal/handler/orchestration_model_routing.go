package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

const (
	modelRoutingPinned       = "pinned"
	modelRoutingCost         = "cost"
	modelRoutingBalanced     = "balanced"
	modelRoutingIntelligence = "intelligence"

	orchestrationModelRouterVersion = 1
)

type orchestrationModelRoutingPolicy struct {
	Mode          string                              `json:"mode"`
	RouterVersion int                                 `json:"router_version"`
	Decisions     []orchestrationModelRoutingDecision `json:"decisions,omitempty"`
}

type orchestrationModelRoutingDecision struct {
	StepKey       string   `json:"step_key"`
	Provider      string   `json:"provider"`
	Model         string   `json:"model,omitempty"`
	ThinkingLevel string   `json:"thinking_level,omitempty"`
	QualityTier   string   `json:"quality_tier"`
	Reason        string   `json:"reason"`
	Signals       []string `json:"signals,omitempty"`
}

type orchestrationModelProfile struct {
	Model    string
	Thinking string
}

type orchestrationProviderProfile struct {
	Efficient  orchestrationModelProfile
	Balanced   orchestrationModelProfile
	Frontier   orchestrationModelProfile
	NativeAuto bool
}

func normalizeModelRoutingMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case modelRoutingPinned:
		return modelRoutingPinned
	case modelRoutingCost:
		return modelRoutingCost
	case modelRoutingBalanced:
		return modelRoutingBalanced
	case modelRoutingIntelligence:
		return modelRoutingIntelligence
	default:
		return ""
	}
}

func orchestrationProviderModels(provider string) (orchestrationProviderProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex":
		return orchestrationProviderProfile{
			Efficient: orchestrationModelProfile{Model: "gpt-5.6-luna", Thinking: "low"},
			Balanced:  orchestrationModelProfile{Model: "gpt-5.6-terra", Thinking: "medium"},
			Frontier:  orchestrationModelProfile{Model: "gpt-5.6-sol", Thinking: "high"},
		}, true
	case "claude":
		return orchestrationProviderProfile{
			Efficient: orchestrationModelProfile{Model: "claude-haiku-4-5-20251001", Thinking: "low"},
			Balanced:  orchestrationModelProfile{Model: "claude-sonnet-4-6", Thinking: "medium"},
			Frontier:  orchestrationModelProfile{Model: "claude-opus-4-8", Thinking: "high"},
		}, true
	case "gemini":
		return orchestrationProviderProfile{
			Efficient: orchestrationModelProfile{Model: "flash"},
			Balanced:  orchestrationModelProfile{Model: "pro"},
			Frontier:  orchestrationModelProfile{Model: "pro"},
		}, true
	case "cursor":
		return orchestrationProviderProfile{
			Efficient:  orchestrationModelProfile{Model: "auto"},
			Balanced:   orchestrationModelProfile{Model: "auto"},
			Frontier:   orchestrationModelProfile{Model: "auto"},
			NativeAuto: true,
		}, true
	default:
		return orchestrationProviderProfile{}, false
	}
}

func orchestrationIssueRoutingSignals(issue db.Issue) (highRisk bool, mechanical bool, signals []string) {
	description := ""
	if issue.Description.Valid {
		description = issue.Description.String
	}
	body := strings.ToLower(strings.TrimSpace(issue.Title + "\n" + description + "\n" + string(issue.AcceptanceCriteria)))
	if issue.Priority == "urgent" {
		highRisk = true
		signals = append(signals, "urgent_priority")
	}
	for _, marker := range []string{
		"security", "authentication", "authorization", "permission", "credential", "secret",
		"payment", "billing", "migration", "schema", "data loss", "delete data", "production",
		"encryption", "concurrency", "race condition", "rollback",
	} {
		if strings.Contains(body, marker) {
			highRisk = true
			signals = append(signals, "risk:"+strings.ReplaceAll(marker, " ", "_"))
			break
		}
	}
	for _, marker := range []string{
		"typo", "copy change", "rename", "readme", "documentation", "docs", "comment",
		"format", "lint", "test only", "add test", "small ui", "css",
	} {
		if strings.Contains(body, marker) {
			mechanical = true
			signals = append(signals, "mechanical:"+strings.ReplaceAll(marker, " ", "_"))
			break
		}
	}
	// A terse title with no supporting description is usually a bounded task.
	// Do not infer "mechanical" from total character count once the author has
	// supplied a real description: broad cross-boundary scopes can still fit in
	// a few hundred characters and need the balanced model.
	if !highRisk && !mechanical && strings.TrimSpace(description) == "" && body != "" && len(body) <= 160 {
		mechanical = true
		signals = append(signals, "short_scope")
	}
	return highRisk, mechanical, signals
}

func adaptiveModelRoute(mode string, issue db.Issue, step orchestrationStepRequest, provider string) orchestrationModelRoutingDecision {
	decision := orchestrationModelRoutingDecision{
		StepKey: step.Key, Provider: strings.ToLower(strings.TrimSpace(provider)), QualityTier: "pinned",
	}
	profile, supported := orchestrationProviderModels(provider)
	if !supported {
		decision.Model = strings.TrimSpace(step.Model)
		decision.Reason = "Provider has no Agora routing profile; preserve the agent pin"
		return decision
	}
	if profile.NativeAuto {
		decision.Model = profile.Balanced.Model
		decision.QualityTier = "provider_auto"
		decision.Reason = "Delegate model selection to the runtime's native Auto router"
		return decision
	}

	highRisk, mechanical, signals := orchestrationIssueRoutingSignals(issue)
	decision.Signals = signals
	tier := "balanced"
	reason := "Use the balanced model for routine agentic work"
	switch {
	case step.Stage == "plan":
		tier = "frontier"
		reason = "Planning controls every downstream step, so use frontier reasoning"
	case mode == modelRoutingIntelligence:
		tier = "frontier"
		reason = "Intelligence-first policy selects frontier capability"
	case highRisk:
		tier = "frontier"
		reason = "Risk signals require frontier capability"
	case step.Stage == "release":
		tier = "efficient"
		reason = "Release is a bounded post-approval operation"
	case mode == modelRoutingCost && step.Stage == "dev" && step.Kind != "integration":
		tier = "efficient"
		reason = "Cost-first policy uses the efficient model for implementation"
	case mode == modelRoutingCost && step.Stage == "qa":
		tier = "efficient"
		reason = "Cost-first policy uses the efficient model for bounded verification"
	case mode == modelRoutingBalanced && mechanical && step.Stage == "dev" && step.Kind != "integration":
		tier = "efficient"
		reason = "Small or mechanical implementation can use the efficient model"
	}

	selected := profile.Balanced
	switch tier {
	case "frontier":
		selected = profile.Frontier
		if step.Stage == "plan" {
			switch decision.Provider {
			case "codex", "claude":
				selected.Thinking = "xhigh"
			}
		}
	case "efficient":
		selected = profile.Efficient
	}
	decision.Model = selected.Model
	decision.ThinkingLevel = selected.Thinking
	decision.QualityTier = tier
	decision.Reason = reason
	return decision
}

func effectiveRoutingAgent(step orchestrationStepRequest, routing orchestrationRouting) pgtype.UUID {
	agentID, _ := parseOptionalUUID(step.AgentID)
	if agentID.Valid || step.HumanOnly {
		return agentID
	}
	if routing.ControllerAgent.Valid {
		return routing.ControllerAgent
	}
	return routing.DevelopmentAgent
}

// applyAdaptiveModelRouting resolves exact provider-native execution pins at
// plan creation. Generated plans may replace roster defaults; custom plans keep
// any explicit model/thinking pair and route only unpinned steps.
func (h *Handler) applyAdaptiveModelRouting(
	ctx context.Context,
	issue db.Issue,
	routing orchestrationRouting,
	mode string,
	steps []orchestrationStepRequest,
	generated bool,
) []orchestrationModelRoutingDecision {
	if mode == modelRoutingPinned {
		return nil
	}
	providers := map[string]string{}
	decisions := make([]orchestrationModelRoutingDecision, 0, len(steps))
	for index := range steps {
		step := &steps[index]
		if step.HumanOnly {
			continue
		}
		if !generated && (strings.TrimSpace(step.Model) != "" || step.ThinkingLevel != nil) {
			decisions = append(decisions, orchestrationModelRoutingDecision{
				StepKey: step.Key, Model: strings.TrimSpace(step.Model), QualityTier: "pinned",
				Reason: "Custom step has an explicit model or thinking pin",
			})
			continue
		}
		agentID := effectiveRoutingAgent(*step, routing)
		if !agentID.Valid {
			continue
		}
		agentKey := uuidToString(agentID)
		provider, cached := providers[agentKey]
		if !cached {
			agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID: agentID, WorkspaceID: issue.WorkspaceID,
			})
			if err == nil && agent.RuntimeID.Valid {
				if runtime, runtimeErr := h.Queries.GetAgentRuntime(ctx, agent.RuntimeID); runtimeErr == nil {
					provider = strings.ToLower(strings.TrimSpace(runtime.Provider))
				}
			}
			providers[agentKey] = provider
		}
		decision := adaptiveModelRoute(mode, issue, *step, provider)
		decisions = append(decisions, decision)
		if decision.QualityTier == "pinned" {
			continue
		}
		step.Model = decision.Model
		thinking := decision.ThinkingLevel
		step.ThinkingLevel = &thinking
	}
	return decisions
}
