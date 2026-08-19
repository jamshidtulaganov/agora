package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Built-in recipes: the flows a team would otherwise have to assemble node by node.
// Installing one writes ORDINARY automation rows — nothing about a recipe-created
// flow is special afterwards, so it can be edited, disabled or deleted like any
// hand-built one. recipe_key is kept only so the gallery can show what is already
// installed instead of offering a duplicate.
//
// Recipe #1 is the review pipeline this deployment already runs behind flags: the
// tracker's Code Review column starts an independent review, and the room hears
// about the outcome. Expressing it as rules is the point of the engine — the same
// behaviour, now visible and editable instead of compiled in.

// automationRecipe is a template: metadata plus the flows it installs.
type automationRecipe struct {
	Key         string                 `json:"key"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Category    string                 `json:"category"`
	Flows       []automationRecipeFlow `json:"flows"`
}

// automationRecipeFlow is one automation row a recipe creates.
type automationRecipeFlow struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	TriggerType string                `json:"trigger_type"`
	Conditions  []automationCondition `json:"conditions"`
	Actions     []automationAction    `json:"actions"`
}

const (
	automationRecipeCodeReview = "code_review_telegram"
	automationRecipeQAFailBack = "qa_fail_back_to_dev"
	automationRecipeStaleNudge = "review_stale_nudge"
)

// automationRecipes is the gallery, most useful first.
func automationRecipes() []automationRecipe {
	return []automationRecipe{
		{
			Key:      automationRecipeCodeReview,
			Title:    "Code review on the review column + Telegram",
			Category: "Review",
			Description: "When the tracker moves a task into Code Review (or the board moves it to In review), " +
				"an independent agent reviews the diff and the team room hears the verdict. A failing review " +
				"sends the task back to To Do for the developer.",
			Flows: []automationRecipeFlow{
				{
					Name:        "Code Review column → run the review",
					Description: "The reviewer reads the branch diff; no merge request is needed yet.",
					TriggerType: automationTriggerStageChanged,
					Conditions: []automationCondition{
						{Field: "stage", Op: automationOpContains, Value: []string{"review", "ревью", "ревю"}},
						{Field: "prev_stage", Op: automationOpNotIn, Value: []string{"Code Review", "In Code Review", "Review"}},
					},
					Actions: []automationAction{
						{Type: automationActionDispatchSlice, Config: map[string]string{"kind": sliceActionRunReview, "agent": "reviewer"}},
						{Type: automationActionSendTelegram, Config: map[string]string{
							"destination": "group",
							"text":        "🔍 {{issue}} — {{title}}\nCode review started (moved into review).",
						}},
					},
				},
				{
					Name:        "Review passed → tell the room",
					Description: "The verdict landed clean. The merge request opening is handled by the review pipeline itself.",
					TriggerType: automationTriggerLabelAttached,
					Conditions: []automationCondition{
						{Field: "label", Op: automationOpEq, Value: "review:pass"},
					},
					Actions: []automationAction{
						{Type: automationActionSendTelegram, Config: map[string]string{
							"destination": "group",
							"text":        "✅ {{issue}} — {{title}}\nCode review passed.",
						}},
					},
				},
				{
					Name:        "Review failed → back to To Do and tell the owner",
					Description: "The developer gets the task back with the reviewer's findings on the timeline.",
					TriggerType: automationTriggerLabelAttached,
					Conditions: []automationCondition{
						{Field: "label", Op: automationOpEq, Value: "review:fail"},
					},
					Actions: []automationAction{
						{Type: automationActionSetStatus, Config: map[string]string{"status": "todo"}},
						{Type: automationActionAssign, Config: map[string]string{"target": "orchestrator"}},
						{Type: automationActionSendTelegram, Config: map[string]string{
							"destination": "owner",
							"text":        "❌ {{issue}} — {{title}}\nCode review failed and the task is back in To Do. Read the reviewer's findings on the issue.",
						}},
						{Type: automationActionSendTelegram, Config: map[string]string{
							"destination": "group",
							"text":        "❌ {{issue}} — {{title}}\nCode review failed → returned to To Do.",
						}},
					},
				},
			},
		},
		{
			Key:      automationRecipeQAFailBack,
			Title:    "QA failure returns the task to the developer",
			Category: "QA",
			Description: "A qa:fail verdict puts the task back in To Do with its owner, and notifies them. " +
				"Use this when you want the QA loop visible as a rule rather than platform behaviour.",
			Flows: []automationRecipeFlow{
				{
					Name:        "qa:fail → back to To Do",
					TriggerType: automationTriggerLabelAttached,
					Conditions: []automationCondition{
						{Field: "label", Op: automationOpEq, Value: "qa:fail"},
					},
					Actions: []automationAction{
						{Type: automationActionSetStatus, Config: map[string]string{"status": "todo"}},
						{Type: automationActionAssign, Config: map[string]string{"target": "orchestrator"}},
						{Type: automationActionSendTelegram, Config: map[string]string{
							"destination": "owner",
							"text":        "🧪 {{issue}} — {{title}}\nQA failed. The task is back in To Do.",
						}},
					},
				},
			},
		},
		{
			Key:         automationRecipeStaleNudge,
			Title:       "Ready for release → nudge the room",
			Category:    "Release",
			Description: "When a task reaches the release column, post it to the team room so the deploy is not forgotten.",
			Flows: []automationRecipeFlow{
				{
					Name:        "Release column → notify",
					TriggerType: automationTriggerStageChanged,
					Conditions: []automationCondition{
						{Field: "stage", Op: automationOpContains, Value: []string{"release", "релиз"}},
					},
					Actions: []automationAction{
						{Type: automationActionSendTelegram, Config: map[string]string{
							"destination": "group",
							"text":        "🚀 {{issue}} — {{title}}\nReady for release.",
						}},
					},
				},
			},
		},
	}
}

func automationRecipeByKey(key string) (automationRecipe, bool) {
	for _, recipe := range automationRecipes() {
		if recipe.Key == strings.TrimSpace(key) {
			return recipe, true
		}
	}
	return automationRecipe{}, false
}

// ListAutomationRecipes handles GET /api/automations/recipes. `installed` tells the
// gallery which recipes this workspace already has, so it offers "view" instead of
// creating a second copy.
func (h *Handler) ListAutomationRecipes(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	installed := map[string]bool{}
	if rows, err := h.Queries.ListAutomationsForWorkspace(r.Context(), parseUUID(workspaceID)); err == nil {
		for _, row := range rows {
			if key := strings.TrimSpace(row.RecipeKey); key != "" {
				installed[key] = true
			}
		}
	}
	recipes := automationRecipes()
	out := make([]map[string]any, 0, len(recipes))
	for _, recipe := range recipes {
		out = append(out, map[string]any{
			"key":         recipe.Key,
			"title":       recipe.Title,
			"description": recipe.Description,
			"category":    recipe.Category,
			"flows":       recipe.Flows,
			"installed":   installed[recipe.Key],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipes": out, "total": len(out)})
}

// InstallAutomationRecipe handles POST /api/automations/recipes/{key}/install.
// Flows are created DISABLED unless the caller opts in, so installing a recipe can
// never start moving a live board before a human has read what it does.
func (h *Handler) InstallAutomationRecipe(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	userID := requestUserID(r)
	recipe, ok := automationRecipeByKey(chi.URLParam(r, "key"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown recipe")
		return
	}
	var req struct {
		ProjectID *string `json:"project_id"`
		Enabled   *bool   `json:"enabled"`
	}
	// A body is optional: installing with defaults is one click.
	_ = json.NewDecoder(r.Body).Decode(&req)

	projectID, ok := h.automationProjectID(w, r, workspaceID, req.ProjectID)
	if !ok {
		return
	}
	enabled := false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	actorID, _ := actorAuthorID(userID)

	created := make([]AutomationResponse, 0, len(recipe.Flows))
	for _, flow := range recipe.Flows {
		if err := validateAutomationRule(flow.TriggerType, flow.Conditions, flow.Actions); err != nil {
			// A broken built-in is a bug in this file, not user input.
			writeError(w, http.StatusInternalServerError, "recipe flow is invalid: "+err.Error())
			return
		}
		conditions, _ := json.Marshal(flow.Conditions)
		actions, _ := json.Marshal(flow.Actions)
		row, err := h.Queries.CreateAutomation(r.Context(), db.CreateAutomationParams{
			WorkspaceID:   parseUUID(workspaceID),
			ProjectID:     projectID,
			Name:          flow.Name,
			Description:   flow.Description,
			Enabled:       enabled,
			TriggerType:   flow.TriggerType,
			TriggerConfig: []byte(`{}`),
			Conditions:    conditions,
			Actions:       actions,
			RecipeKey:     recipe.Key,
			CreatedByType: "member",
			CreatedByID:   actorID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to install recipe")
			return
		}
		created = append(created, automationToResponse(row))
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"recipe":      recipe.Key,
		"automations": created,
		"enabled":     enabled,
	})
}
