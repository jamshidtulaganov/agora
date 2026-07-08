package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var qaManifestBlockRe = regexp.MustCompile("(?s)```qa-manifest\\s*\\n(.*?)```")

// parseQAManifestBlock extracts a ```qa-manifest``` fenced JSON object from an
// agent's output. ok=false when the block is absent or unparseable.
func parseQAManifestBlock(content string) (map[string]any, bool) {
	m := qaManifestBlockRe.FindStringSubmatch(content)
	if m == nil {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &obj); err != nil {
		return nil, false
	}
	return obj, len(obj) > 0
}

// CaptureQAManifest ADDITIVELY merges a ```qa-manifest``` block emitted by a
// just-completed task into project.settings.qa_manifest: routes and flows the
// task exercised are added, existing entries are never overwritten (human or
// prior-agent curation is preserved), so the manifest grows richer with every
// done task. No-op when nothing new is discovered — no write, no churn. Written
// key-scoped via SetProjectSettingKey so it can't clobber sibling settings.
func (s *TaskService) CaptureQAManifest(ctx context.Context, issue db.Issue, content string, authorID pgtype.UUID) {
	if !issue.ProjectID.Valid {
		return
	}
	delta, ok := parseQAManifestBlock(content)
	if !ok {
		return
	}
	project, err := s.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil {
		return
	}
	settings := map[string]json.RawMessage{}
	if len(project.Settings) > 0 {
		_ = json.Unmarshal(project.Settings, &settings)
	}
	existing := map[string]any{}
	if raw, ok := settings["qa_manifest"]; ok {
		_ = json.Unmarshal(raw, &existing)
	}
	added := mergeQAManifestAdditive(existing, delta)
	if added == 0 {
		return
	}
	blob, err := json.Marshal(existing)
	if err != nil {
		return
	}
	if _, err := s.Queries.SetProjectSettingKey(ctx, db.SetProjectSettingKeyParams{
		ID:          issue.ProjectID,
		WorkspaceID: issue.WorkspaceID,
		Key:         "qa_manifest",
		Value:       blob,
	}); err != nil {
		slog.Warn("capture qa manifest: settings write failed",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	slog.Info("qa manifest enriched from done task",
		"issue_id", util.UUIDToString(issue.ID), "added", added)
}

// mergeQAManifestAdditive folds delta into dst in place: routes (map) gain keys
// they lack; flows (array) gain entries whose name+path aren't already present;
// any other top-level key is filled only when absent. Returns how many new
// entries were added so the caller can skip a no-op write.
func mergeQAManifestAdditive(dst, delta map[string]any) int {
	added := 0

	if dr, ok := delta["routes"].(map[string]any); ok && len(dr) > 0 {
		routes, _ := dst["routes"].(map[string]any)
		if routes == nil {
			routes = map[string]any{}
		}
		for k, v := range dr {
			if _, exists := routes[k]; !exists {
				routes[k] = v
				added++
			}
		}
		dst["routes"] = routes
	}

	if df, ok := delta["flows"].([]any); ok && len(df) > 0 {
		flows, _ := dst["flows"].([]any)
		seen := map[string]bool{}
		for _, f := range flows {
			if fm, ok := f.(map[string]any); ok {
				seen[qaFlowKey(fm)] = true
			}
		}
		for _, f := range df {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			if k := qaFlowKey(fm); k != "|" && !seen[k] {
				flows = append(flows, fm)
				seen[k] = true
				added++
			}
		}
		dst["flows"] = flows
	}

	for k, v := range delta {
		if k == "routes" || k == "flows" {
			continue
		}
		if _, exists := dst[k]; !exists {
			dst[k] = v
			added++
		}
	}
	return added
}

func qaFlowKey(f map[string]any) string {
	name, _ := f["name"].(string)
	path, _ := f["path"].(string)
	return strings.TrimSpace(strings.ToLower(name)) + "|" + strings.TrimSpace(strings.ToLower(path))
}
