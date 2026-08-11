package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestAdaptiveModelRouteCodexByStepAndPolicy(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		issue    db.Issue
		step     orchestrationStepRequest
		model    string
		thinking string
		tier     string
	}{
		{
			name: "planning always uses frontier reasoning", mode: modelRoutingCost,
			issue: db.Issue{Title: "Rename a settings label"},
			step:  orchestrationStepRequest{Key: "plan", Stage: "plan"},
			model: "gpt-5.6-sol", thinking: "xhigh", tier: "frontier",
		},
		{
			name: "cost uses efficient implementation", mode: modelRoutingCost,
			issue: db.Issue{Title: "Add account preferences"},
			step:  orchestrationStepRequest{Key: "dev", Stage: "dev"},
			model: "gpt-5.6-luna", thinking: "low", tier: "efficient",
		},
		{
			name: "balanced uses efficient for small mechanical work", mode: modelRoutingBalanced,
			issue: db.Issue{Title: "Fix README typo"},
			step:  orchestrationStepRequest{Key: "dev-docs", Stage: "dev"},
			model: "gpt-5.6-luna", thinking: "low", tier: "efficient",
		},
		{
			name: "balanced uses terra for broad implementation", mode: modelRoutingBalanced,
			issue: db.Issue{Title: "Build the workspace notification center", Description: pgtype.Text{String: "Add API endpoints, delivery preferences, event aggregation, inbox pagination, UI states, tests, and operational metrics across the existing application boundaries.", Valid: true}},
			step:  orchestrationStepRequest{Key: "dev", Stage: "dev"},
			model: "gpt-5.6-terra", thinking: "medium", tier: "balanced",
		},
		{
			name: "risk overrides cost savings", mode: modelRoutingCost,
			issue: db.Issue{Title: "Migrate authentication schema safely"},
			step:  orchestrationStepRequest{Key: "dev-backend", Stage: "dev"},
			model: "gpt-5.6-sol", thinking: "high", tier: "frontier",
		},
		{
			name: "intelligence uses frontier verification", mode: modelRoutingIntelligence,
			issue: db.Issue{Title: "Verify notification delivery"},
			step:  orchestrationStepRequest{Key: "qa", Stage: "qa"},
			model: "gpt-5.6-sol", thinking: "high", tier: "frontier",
		},
		{
			name: "release is efficient after approval", mode: modelRoutingBalanced,
			issue: db.Issue{Title: "Ship account preferences"},
			step:  orchestrationStepRequest{Key: "release", Stage: "release"},
			model: "gpt-5.6-luna", thinking: "low", tier: "efficient",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adaptiveModelRoute(tt.mode, tt.issue, tt.step, "codex")
			if got.Model != tt.model || got.ThinkingLevel != tt.thinking || got.QualityTier != tt.tier {
				t.Fatalf("route = model %q thinking %q tier %q; want %q %q %q", got.Model, got.ThinkingLevel, got.QualityTier, tt.model, tt.thinking, tt.tier)
			}
			if got.Reason == "" {
				t.Fatal("route must record an audit reason")
			}
		})
	}
}

func TestAdaptiveModelRouteProviderProfiles(t *testing.T) {
	issue := db.Issue{Title: "Plan a multi-stage implementation"}
	step := orchestrationStepRequest{Key: "plan", Stage: "plan"}
	tests := []struct {
		provider string
		model    string
		thinking string
		tier     string
	}{
		{provider: "claude", model: "claude-opus-4-8", thinking: "xhigh", tier: "frontier"},
		{provider: "gemini", model: "pro", tier: "frontier"},
		{provider: "antigravity", model: "gemini-3.1-pro-high", tier: "frontier"},
		{provider: "cursor", model: "auto", tier: "provider_auto"},
		{provider: "unknown", tier: "pinned"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := adaptiveModelRoute(modelRoutingBalanced, issue, step, tt.provider)
			if got.Model != tt.model || got.ThinkingLevel != tt.thinking || got.QualityTier != tt.tier {
				t.Fatalf("route = %#v", got)
			}
		})
	}
}

func TestNormalizeModelRoutingModeRejectsUnknownValues(t *testing.T) {
	if got := normalizeModelRoutingMode(" BALANCED "); got != modelRoutingBalanced {
		t.Fatalf("normalized mode = %q", got)
	}
	if got := normalizeModelRoutingMode("automatic"); got != "" {
		t.Fatalf("unknown mode should be rejected, got %q", got)
	}
}

func TestResolveModelRoutingModePrecedence(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		project   string
		squad     string
		want      string
	}{
		{name: "request overrides project and squad", requested: "intelligence", project: "cost", squad: "balanced", want: modelRoutingIntelligence},
		{name: "project overrides squad", project: "cost", squad: "balanced", want: modelRoutingCost},
		{name: "squad supplies auto default", squad: "balanced", want: modelRoutingBalanced},
		{name: "legacy default stays pinned", want: modelRoutingPinned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveModelRoutingMode(tt.requested, tt.project, tt.squad); got != tt.want {
				t.Fatalf("resolveModelRoutingMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
