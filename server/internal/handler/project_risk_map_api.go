package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// THE RISK MAP WRITE ENDPOINT (docs/orchestration-upgrade-plan.md §A1.2).
//
// The risk map has driven five pipeline decisions since it shipped and has
// never had a way to author it: project_risk_map.go said so itself, and the key
// was edited straight into project.settings jsonb by hand. Without a write
// endpoint "risk as a first-class object" is a slogan.
//
// Three properties this endpoint has to have, each for a specific reason:
//
//   - KEY-SCOPED WRITE. It goes through SetProjectSettingKey (jsonb_set on the
//     single `risk_map` path), never a read-modify-write of the whole settings
//     blob — the risk-map file explicitly warns about clobbering siblings, and
//     UpdateProject additionally NULLs five non-COALESCEd columns when handed a
//     partial struct.
//   - OWNER/ADMIN + HUMAN ONLY. The map decides what an agent may merge without
//     a human. An agent that can rewrite it can grant itself permission, so the
//     route carries RequireHumanActor and the handler requires owner/admin.
//   - VALIDATED, LOUDLY. A malformed map is dropped at read time (and logged),
//     which means a bad save would leave an admin believing a safety control is
//     in force while nothing enforces it. Everything is checked here, up front,
//     with a 400 that names the offending entry.
//
// Re-tiering is NOT retroactive by design: the tier is computed on read
// (risk_tier.go), so the next read of every issue in the project already uses
// the new map. There is nothing to backfill and nothing stored to go stale.

// riskMapEntryMaxPaths caps the globs on one module. A module needing more than
// this is two modules, or a file inventory pretending to be a tiering.
const (
	riskMapEntryMaxPaths = 40
	riskMapModuleMaxLen  = 80
	riskMapPathMaxLen    = 200
	riskMapTextMaxLen    = 400
)

// setProjectRiskMapRequest is the wire shape: an object with one key, not a
// bare array, so the payload has somewhere to grow and a client schema has
// something to name.
type setProjectRiskMapRequest struct {
	RiskMap []riskMapEntry `json:"risk_map"`
}

// projectRiskMapResponse is what both GET and PUT return.
type projectRiskMapResponse struct {
	ProjectID string `json:"project_id"`
	// Configured is false when the project has no risk map at all — the state
	// in which issueRiskTier has no opinion and the API tier is `unclassified`.
	Configured bool           `json:"configured"`
	RiskMap    []riskMapEntry `json:"risk_map"`
	// DefaultTier is what a path NO glob matches resolves to. It is `guarded`
	// and it is not configurable: unknown must never mean safe.
	DefaultTier string `json:"default_tier"`
	// Tiers is the accepted vocabulary, so a settings UI does not hardcode it.
	Tiers []string `json:"tiers"`
	// MaxEntries is the cap the validator enforces AND the cap the agent brief
	// renders — they are the same number on purpose (see the validator).
	MaxEntries int `json:"max_entries"`
}

// GetProjectRiskMap handles GET /api/projects/{id}/risk-map. Any member of the
// project's workspace may read the tiering — it is the policy their changes are
// judged against, and hiding it from the people it governs helps nobody.
//
// A project with no map returns configured=false and an EMPTY array rather than
// a 404: "this project has no tiering" is the answer, and it is the answer the
// settings panel needs in order to offer the first one.
func (h *Handler) GetProjectRiskMap(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForRiskMap(w, r, false)
	if !ok {
		return
	}
	entries, configured := parseProjectRiskMap(project.Settings, project.ID)
	writeJSON(w, http.StatusOK, riskMapResponseFor(project, entries, configured))
}

// SetProjectRiskMap handles PUT /api/projects/{id}/risk-map.
func (h *Handler) SetProjectRiskMap(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForRiskMap(w, r, true)
	if !ok {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // a tiering of modules is small
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	var req setProjectRiskMapRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "risk map is not valid JSON: "+err.Error())
		return
	}

	entries, verr := validateRiskMapEntries(req.RiskMap)
	if verr != "" {
		writeError(w, http.StatusBadRequest, verr)
		return
	}

	encoded, err := json.Marshal(entries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode risk map")
		return
	}
	// KEY-SCOPED: jsonb_set on settings->'risk_map' only. Sibling keys
	// (qa_manifest, config, design context, bitrix stamps) are untouched.
	updated, err := h.Queries.SetProjectSettingKey(r.Context(), db.SetProjectSettingKeyParams{
		ID:          project.ID,
		WorkspaceID: project.WorkspaceID,
		Key:         "risk_map",
		Value:       encoded,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save the risk map")
		return
	}
	writeJSON(w, http.StatusOK, riskMapResponseFor(updated, entries, len(entries) > 0))
}

func riskMapResponseFor(project db.Project, entries []riskMapEntry, configured bool) projectRiskMapResponse {
	if entries == nil {
		entries = []riskMapEntry{}
	}
	return projectRiskMapResponse{
		ProjectID:   uuidToString(project.ID),
		Configured:  configured,
		RiskMap:     entries,
		DefaultTier: riskTierGuarded,
		Tiers:       []string{riskTierCritical, riskTierGuarded, riskTierSafe},
		MaxEntries:  riskMapMaxEntries,
	}
}

// loadProjectForRiskMap resolves {id} inside the CALLER'S workspace and applies
// the role gate. Two separate checks, deliberately: the workspace fence comes
// from the request's own workspace header (so a project id from another tenant
// simply does not exist here), and the role check then runs against that same
// workspace.
func (h *Handler) loadProjectForRiskMap(w http.ResponseWriter, r *http.Request, write bool) (db.Project, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return db.Project{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return db.Project{}, false
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return db.Project{}, false
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return db.Project{}, false
	}
	roles := []string{"owner", "admin", "member"}
	if write {
		// Authoring the tiering decides what may merge without a human.
		roles = []string{"owner", "admin"}
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(project.WorkspaceID), "project not found", roles...); !ok {
		return db.Project{}, false
	}
	return project, true
}

// validateRiskMapEntries checks and NORMALIZES the submitted map. It returns the
// cleaned entries, or a human-readable reason the save was refused.
//
// Normalization is part of the contract, not a convenience: the stored map is
// what both the agent brief renders and what the server glob-matches, so a
// trailing slash or a "./" prefix must mean the same thing in both places.
func validateRiskMapEntries(entries []riskMapEntry) ([]riskMapEntry, string) {
	if len(entries) == 0 {
		// An empty array is a legitimate request: "remove the tiering". It is
		// stored as an empty array, which parseProjectRiskMap reports as
		// unconfigured — the project returns to having no opinion.
		return []riskMapEntry{}, ""
	}
	// The cap is the SAME number the brief renderer truncates at. Storing
	// entries that never reach the agent would leave an admin believing a
	// module is tiered while every run is briefed without it.
	if len(entries) > riskMapMaxEntries {
		return nil, "a risk map is a tiering of modules, not a file inventory: at most " +
			strconv.Itoa(riskMapMaxEntries) + " entries (got " + strconv.Itoa(len(entries)) + ")"
	}

	seen := map[string]bool{}
	out := make([]riskMapEntry, 0, len(entries))
	for i, e := range entries {
		where := "entry " + strconv.Itoa(i+1)
		module := strings.TrimSpace(e.Module)
		if module == "" {
			return nil, where + ": module is required"
		}
		if len([]rune(module)) > riskMapModuleMaxLen {
			return nil, where + " (" + module + "): module name is too long"
		}
		key := strings.ToLower(module)
		if seen[key] {
			// Duplicates make issueRiskOwners ambiguous (a module: label would
			// resolve to two owners) and hide one of the two tierings.
			return nil, where + " (" + module + "): duplicate module"
		}
		seen[key] = true

		tier := normalizeRiskMapTier(e.Tier)
		if raw := strings.TrimSpace(e.Tier); raw != "" && !strings.EqualFold(raw, tier) {
			return nil, where + " (" + module + "): tier must be critical, guarded or safe (got " + raw + ")"
		}

		if len(e.Paths) == 0 {
			return nil, where + " (" + module + "): at least one path glob is required"
		}
		if len(e.Paths) > riskMapEntryMaxPaths {
			return nil, where + " (" + module + "): at most " + strconv.Itoa(riskMapEntryMaxPaths) + " path globs"
		}
		paths := make([]string, 0, len(e.Paths))
		pathSeen := map[string]bool{}
		for _, raw := range e.Paths {
			p := normalizeRiskPath(raw)
			if p == "" {
				return nil, where + " (" + module + "): empty path glob"
			}
			if len(p) > riskMapPathMaxLen {
				return nil, where + " (" + module + "): path glob is too long: " + p
			}
			// A glob that path.Match cannot compile (an unterminated character
			// class, say) matches NOTHING at runtime — every file under it would
			// silently fall through to the guarded default while the map claims
			// it is critical. Refuse it here where someone can see the message.
			for _, seg := range strings.Split(p, "/") {
				if seg == "**" {
					continue
				}
				if _, err := path.Match(seg, "probe"); err != nil {
					return nil, where + " (" + module + "): malformed path glob: " + p
				}
			}
			if pathSeen[p] {
				continue // a duplicate glob is harmless; drop it silently
			}
			pathSeen[p] = true
			paths = append(paths, p)
		}

		owner := strings.TrimSpace(e.Owner)
		notes := strings.TrimSpace(e.Notes)
		if len([]rune(owner)) > riskMapTextMaxLen || len([]rune(notes)) > riskMapTextMaxLen {
			return nil, where + " (" + module + "): owner/notes are too long"
		}
		out = append(out, riskMapEntry{
			Module: module,
			Tier:   tier,
			Paths:  paths,
			Owner:  owner,
			Notes:  notes,
		})
	}
	return out, ""
}
