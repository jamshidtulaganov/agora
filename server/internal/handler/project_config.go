package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/config"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-project pipeline config. A project may override the ProjectScoped keys
// (QA / sprint / review / automation behavior) for its OWN issues, stored under
// project.settings.config. The scoped config.*From resolvers layer this map
// above the instance value: project > instance override > env > default. This
// is what lets one project run auto-QA + fail-autoroute while another opts out,
// without an operator touching instance-wide Settings → Configs.

// projectConfigOverrides reads an issue's project-scoped config overrides.
// Returns nil when the issue has no project, the config object is absent /
// malformed, or none of its keys are project-scoped — every such case resolves
// to the instance value (fail-open: a bad override object must not wedge the
// pipeline). Workspace-scoped read, fail-closed on tenant (mirrors
// projectRiskMap — issue.project_id is a plain FK with no same-workspace DB
// constraint).
func (h *Handler) projectConfigOverrides(ctx context.Context, issue db.Issue) map[string]string {
	if !issue.ProjectID.Valid {
		return nil
	}
	project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          issue.ProjectID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return nil
	}
	return parseProjectConfigOverrides(project.Settings)
}

// parseProjectConfigOverrides extracts settings.config → {key: value}, keeping
// ONLY keys that are actually ProjectScoped (a stray or since-removed key is
// ignored, never silently applied). Values round-trip as their JSON-string form
// ("true" / "24"); a bare bool/number is tolerated too.
func parseProjectConfigOverrides(settings []byte) map[string]string {
	if len(settings) == 0 {
		return nil
	}
	var s struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if json.Unmarshal(settings, &s) != nil || len(s.Config) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.Config))
	for k, raw := range s.Config {
		if !config.IsProjectScoped(k) {
			continue
		}
		var str string
		if json.Unmarshal(raw, &str) == nil {
			out[k] = str
		} else {
			out[k] = strings.TrimSpace(string(raw))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// projectConfigEntry is one row in the project pipeline-config editor. Same
// shape as instanceConfigEntry but the source can also be "project".
type projectConfigEntry struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Category    string `json:"category"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Value       string `json:"value"`
	// Source is "project" | "override" (instance) | "env" | "default".
	Source string `json:"source"`
	// OverriddenByProject is true when this project sets its own value —
	// the UI shows a Reset affordance only then.
	OverriddenByProject bool `json:"overridden_by_project"`
}

// loadProjectForConfig resolves the {id} project within the caller's workspace.
// Returns ok=false (and writes the response) on a bad id or a missing project.
func (h *Handler) loadProjectForConfig(w http.ResponseWriter, r *http.Request) (db.Project, string, bool) {
	wsID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return db.Project{}, "", false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace id")
	if !ok {
		return db.Project{}, "", false
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          idUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return db.Project{}, "", false
	}
	return project, wsID, true
}

// GetProjectConfig lists the project-scopable keys with their effective value
// and source for THIS project. Any workspace member may read (the UI needs it).
func (h *Handler) GetProjectConfig(w http.ResponseWriter, r *http.Request) {
	project, wsID, ok := h.loadProjectForConfig(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, wsID, "project not found"); !ok {
		return
	}
	overrides := parseProjectConfigOverrides(project.Settings)
	defs := config.ProjectScopedRegistry()
	entries := make([]projectConfigEntry, 0, len(defs))
	for _, d := range defs {
		_, has := overrides[d.Key]
		entries = append(entries, projectConfigEntry{
			Key:                 d.Key,
			Kind:                string(d.Kind),
			Category:            d.Category,
			Label:               d.Label,
			Description:         d.Description,
			Value:               config.ResolveFrom(overrides, d.Key),
			Source:              config.SourceFrom(overrides, d.Key),
			OverriddenByProject: has,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": entries})
}

type setProjectConfigRequest struct {
	Value string `json:"value"`
}

// SetProjectConfig upserts a per-project override for one key. Owner/admin only.
// Rejects non-project-scoped keys and values invalid for the key's kind.
func (h *Handler) SetProjectConfig(w http.ResponseWriter, r *http.Request) {
	project, wsID, ok := h.loadProjectForConfig(w, r)
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, wsID, "project not found", "owner", "admin")
	if !ok {
		return
	}
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	def, found := config.Lookup(key)
	if !found || !def.ProjectScoped {
		writeError(w, http.StatusBadRequest, "unknown or non-project-scoped config key")
		return
	}
	var req setProjectConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	normalized, verr := normalizeConfigValue(def, strings.TrimSpace(req.Value))
	if verr != "" {
		writeError(w, http.StatusBadRequest, verr)
		return
	}
	updated, err := h.Queries.SetProjectConfigKey(r.Context(), db.SetProjectConfigKeyParams{
		ID:          project.ID,
		WorkspaceID: project.WorkspaceID,
		Key:         key,
		Value:       normalized,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save project config")
		return
	}
	_ = member
	overrides := parseProjectConfigOverrides(updated.Settings)
	writeJSON(w, http.StatusOK, map[string]any{
		"key":                   key,
		"value":                 config.ResolveFrom(overrides, key),
		"source":                config.SourceFrom(overrides, key),
		"overridden_by_project": true,
	})
}

// ResetProjectConfig drops a per-project override, reverting the key to the
// instance value. Owner/admin only.
func (h *Handler) ResetProjectConfig(w http.ResponseWriter, r *http.Request) {
	project, wsID, ok := h.loadProjectForConfig(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, wsID, "project not found", "owner", "admin"); !ok {
		return
	}
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	if def, found := config.Lookup(key); !found || !def.ProjectScoped {
		writeError(w, http.StatusBadRequest, "unknown or non-project-scoped config key")
		return
	}
	updated, err := h.Queries.DeleteProjectConfigKey(r.Context(), db.DeleteProjectConfigKeyParams{
		ID:          project.ID,
		WorkspaceID: project.WorkspaceID,
		Key:         key,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset project config")
		return
	}
	overrides := parseProjectConfigOverrides(updated.Settings)
	writeJSON(w, http.StatusOK, map[string]any{
		"key":                   key,
		"value":                 config.ResolveFrom(overrides, key),
		"source":                config.SourceFrom(overrides, key),
		"overridden_by_project": false,
	})
}
