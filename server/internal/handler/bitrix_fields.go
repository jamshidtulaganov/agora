package handler

import (
	"net/http"
	"sort"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
)

// The portal's own task-field catalogue.
//
// Exists because a portal's custom fields are unguessable. SalesDoctor's
// scoring fields are named UF_AUTO_124312871720 ("Reach"),
// UF_AUTO_809721135658 ("RICE") and so on — an agent asked "what is our RICE
// spread" has no way to reach them without being told the mapping, and the
// mapping changes whenever someone adds a field in the admin UI.
//
// Returning the catalogue lets the caller discover what exists instead of
// working from a stale hardcoded list. Read-only metadata: no task data, no
// credential.

type bitrixFieldResponse struct {
	// Name is the REST field name, e.g. DEADLINE or UF_AUTO_809721135658.
	Name string `json:"name"`
	// Title is the human label shown in Bitrix, e.g. "Крайний срок", "RICE".
	// Often empty for internal fields, which is a fair signal that a field is
	// plumbing rather than something to report on.
	Title string `json:"title,omitempty"`
	Type  string `json:"type,omitempty"`
	// Custom marks a portal-defined field (UF_*) as opposed to a Bitrix one.
	// The distinction matters: standard fields are stable across portals,
	// custom ones exist only here and only until someone deletes them.
	Custom bool `json:"custom"`
}

// ListBitrixFields handles GET /api/bitrix/fields.
func (h *Handler) ListBitrixFields(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}

	raw, err := bitrix.NewClient(bitrixWebhookURL()).GetTaskFields(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read task fields from Bitrix")
		return
	}

	out := make([]bitrixFieldResponse, 0, len(raw))
	for name, def := range raw {
		out = append(out, bitrixFieldResponse{
			Name:   name,
			Title:  def.Title,
			Type:   def.Type,
			Custom: strings.HasPrefix(name, "UF_"),
		})
	}
	// Stable order so a caller diffing two calls sees real changes, not map
	// iteration noise.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"fields": out, "count": len(out)})
}
