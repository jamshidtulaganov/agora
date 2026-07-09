package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/config"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// instanceConfigEntry is one row in the Settings → Configs list.
type instanceConfigEntry struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"` // bool | int | string | secret
	Category    string `json:"category"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Editable    bool   `json:"editable"`
	// Value is the effective value for editable keys; empty for secrets.
	Value string `json:"value"`
	// Source is "override" | "env" | "default" for editable keys.
	Source string `json:"source,omitempty"`
	// SecretSet reports whether a secret has a value, without exposing it.
	SecretSet bool `json:"secret_set,omitempty"`
}

// requireInstanceConfigAdmin resolves the caller and requires workspace-owner
// role. Instance config is global, but the access model is "a workspace owner"
// (SalesDoctor runs effectively one org). The caller's workspace comes from the
// X-Workspace-ID the request is scoped to.
func (h *Handler) requireInstanceConfigAdmin(w http.ResponseWriter, r *http.Request) bool {
	wsID := ctxWorkspaceID(r.Context())
	if wsID == "" {
		wsID = r.Header.Get("X-Workspace-ID")
	}
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace context required")
		return false
	}
	member, ok := h.workspaceMember(w, r, wsID)
	if !ok {
		return false
	}
	if member.Role != "owner" {
		writeError(w, http.StatusForbidden, "only a workspace owner can manage instance configuration")
		return false
	}
	return true
}

// GetInstanceConfig returns the full config catalog with effective values.
func (h *Handler) GetInstanceConfig(w http.ResponseWriter, r *http.Request) {
	if !h.requireInstanceConfigAdmin(w, r) {
		return
	}
	entries := make([]instanceConfigEntry, 0, len(config.Registry))
	for _, d := range config.Registry {
		e := instanceConfigEntry{
			Key:         d.Key,
			Kind:        string(d.Kind),
			Category:    d.Category,
			Label:       d.Label,
			Description: d.Description,
			Editable:    d.Editable(),
		}
		if d.Kind == config.KindSecret {
			e.SecretSet = config.SecretIsSet(d.Key)
		} else {
			e.Value = config.Resolve(d.Key)
			e.Source = config.Source(d.Key)
		}
		entries = append(entries, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": entries})
}

type setInstanceConfigRequest struct {
	Value string `json:"value"`
}

// SetInstanceConfig upserts an override for one key. Owner-only, human-only.
// Rejects unknown keys, secrets, and values invalid for the key's kind.
func (h *Handler) SetInstanceConfig(w http.ResponseWriter, r *http.Request) {
	if !h.requireInstanceConfigAdmin(w, r) {
		return
	}
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	def, ok := config.Lookup(key)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown config key")
		return
	}
	if !def.Editable() {
		writeError(w, http.StatusForbidden, "this key is managed via secrets and cannot be edited here")
		return
	}
	var req setInstanceConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	value := strings.TrimSpace(req.Value)
	normalized, verr := normalizeConfigValue(def, value)
	if verr != "" {
		writeError(w, http.StatusBadRequest, verr)
		return
	}

	member, _ := ctxMember(r.Context())
	if _, err := h.Queries.UpsertInstanceConfig(r.Context(), db.UpsertInstanceConfigParams{
		Key:       key,
		Value:     normalized,
		UpdatedBy: member.UserID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}
	config.NotifySet(key, normalized)
	writeJSON(w, http.StatusOK, map[string]any{
		"key": key, "value": config.Resolve(key), "source": config.Source(key),
	})
}

// ResetInstanceConfig removes the override, reverting the key to env/default.
func (h *Handler) ResetInstanceConfig(w http.ResponseWriter, r *http.Request) {
	if !h.requireInstanceConfigAdmin(w, r) {
		return
	}
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	if _, ok := config.Lookup(key); !ok {
		writeError(w, http.StatusBadRequest, "unknown config key")
		return
	}
	if err := h.Queries.DeleteInstanceConfig(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset config")
		return
	}
	config.NotifyDelete(key)
	writeJSON(w, http.StatusOK, map[string]any{
		"key": key, "value": config.Resolve(key), "source": config.Source(key),
	})
}

// normalizeConfigValue validates value against the def's kind and returns the
// canonical stored form, or a non-empty error message.
func normalizeConfigValue(def config.Def, value string) (normalized, errMsg string) {
	switch def.Kind {
	case config.KindBool:
		switch strings.ToLower(value) {
		case "true", "1", "on", "yes":
			return "true", ""
		case "false", "0", "off", "no", "":
			return "false", ""
		default:
			return "", "value must be a boolean"
		}
	case config.KindInt:
		if value == "" {
			return "", "value is required"
		}
		if _, err := strconv.Atoi(value); err != nil {
			return "", "value must be an integer"
		}
		return value, ""
	default: // string
		return value, ""
	}
}
