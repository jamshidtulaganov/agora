package handler

import (
	"net/http"
	"strconv"
	"time"
)

// ListMyAssistantArtifacts lists the caller's artifacts across conversations.
// Bodies are fetched separately. Session ownership is the authorization source.
func (h *Handler) ListMyAssistantArtifacts(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000000 {
			writeError(w, http.StatusBadRequest, "invalid artifact offset")
			return
		}
	}
	rows, err := h.DB.Query(r.Context(), `SELECT a.id::text,a.session_id::text,a.title,a.kind,a.version,a.created_at,a.updated_at
		FROM assistant_artifact a JOIN assistant_session s ON s.id=a.session_id
		WHERE s.user_id=$1 ORDER BY a.updated_at DESC,a.id DESC LIMIT 40 OFFSET $2`, userID, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load artifacts")
		return
	}
	defer rows.Close()
	items := make([]AssistantArtifactSummaryResponse, 0)
	for rows.Next() {
		var item AssistantArtifactSummaryResponse
		var created, updated time.Time
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Title, &item.Kind, &item.Version, &created, &updated); err != nil {
			writeError(w, http.StatusInternalServerError, "could not load artifacts")
			return
		}
		item.CreatedAt, item.UpdatedAt = created.UTC().Format(time.RFC3339), updated.UTC().Format(time.RFC3339)
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "could not load artifacts")
		return
	}
	writeJSON(w, http.StatusOK, items)
}
