package handler

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/runtrace"
)

// ExportRunDataset returns the workspace's agent run traces as fine-tuning
// training examples ({input, output, outcome}). Workspace-scoped via
// X-Workspace-ID and gated by RequireWorkspaceMember. Optional filters:
//
//	?outcome=accepted|corrected|rejected   (omitted = all)
//	?limit=100 (1..1000)   ?offset=0
//
// The consumer paginates and writes JSONL client-side. Only labeled
// (non-pending) rows are typically useful; pass ?outcome=accepted for an
// SFT set or fetch all and weight by the outcome label.
func (h *Handler) ExportRunDataset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	var outcome pgtype.Text
	if o := r.URL.Query().Get("outcome"); o != "" {
		outcome = pgtype.Text{String: o, Valid: true}
	}

	examples, err := runtrace.BuildExamples(ctx, h.Queries, runtrace.ExportParams{
		WorkspaceID: wsUUID,
		Outcome:     outcome,
		Limit:       int32(datasetClampInt(r, "limit", 100, 1, 1000)),
		Offset:      int32(datasetClampInt(r, "offset", 0, 0, 1<<30)),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build dataset")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"examples": examples,
		"count":    len(examples),
	})
}

// datasetClampInt reads a non-negative int query param, falling back to def on
// absence/parse-error and clamping into [min, max].
func datasetClampInt(r *http.Request, key string, def, min, max int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}
