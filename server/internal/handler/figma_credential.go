package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The workspace Figma credential lets agents read Figma designs referenced by
// issues: the PAT is sealed at rest with a secretbox loaded from
// AGORA_FIGMA_SECRET_KEY and decrypted only into the per-task mcp_config env
// at claim time (see injectFigmaMcpCreds). If the key is unset the endpoints
// fail closed (503) rather than store plaintext.

var (
	figmaBoxOnce sync.Once
	figmaBoxVal  *secretbox.Box
	figmaBoxErr  error
)

func figmaCredentialBox() (*secretbox.Box, error) {
	figmaBoxOnce.Do(func() {
		key, err := secretbox.LoadKey("AGORA_FIGMA_SECRET_KEY")
		if err != nil {
			figmaBoxErr = err
			return
		}
		figmaBoxVal, figmaBoxErr = secretbox.New(key)
	})
	return figmaBoxVal, figmaBoxErr
}

// figmaAPIBase is a var so tests can point the save-time probe at a stub.
var figmaAPIBase = "https://api.figma.com"

// probeFileKeyRe constrains the optional seat-probe file key to the same
// charset figmaURLRe extracts — a crafted value must not be able to redirect
// the server-side probe (e.g. `x/../../v1/teams/…`) to arbitrary
// api.figma.com endpoints with the workspace token.
var probeFileKeyRe = regexp.MustCompile(`^[A-Za-z0-9]{10,}$`)

type figmaCredentialStatusResponse struct {
	Configured   bool   `json:"configured"`
	Label        string `json:"label,omitempty"`
	TokenLast4   string `json:"token_last4,omitempty"`
	TokenKind    string `json:"token_kind,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	ExpiringSoon bool   `json:"expiring_soon,omitempty"`
	SeatProbe    string `json:"seat_probe,omitempty"`
	ProbeStatus  string `json:"probe_status,omitempty"`
	ProbedAt     string `json:"probed_at,omitempty"`
}

type putFigmaCredentialRequest struct {
	Token        string `json:"token"`
	Label        string `json:"label"`
	ExpiresAt    string `json:"expires_at"`     // RFC3339 or YYYY-MM-DD; optional
	ProbeFileKey string `json:"probe_file_key"` // optional Tier-1 seat probe target
}

// probeFigmaToken checks the token against GET /v1/me and returns the HTTP
// status (0 with reachable=false on transport errors). Classification into
// invalid-vs-outage lives in classifyFigmaProbe so it is unit-testable.
func probeFigmaToken(ctx context.Context, token string) (status int, reachable bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, figmaAPIBase+"/v1/me", nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("X-Figma-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, true
}

// classifyFigmaProbe maps a /v1/me probe outcome to the stored probe_status
// and whether the save must be rejected. Only a definite auth rejection
// (401/403) counts as an invalid token; 429 and 5xx are Figma-side conditions
// — a Figma outage must not block saving a credential, so those save with
// probe_status "unreachable" and the nightly probe revisits them.
func classifyFigmaProbe(status int, reachable bool) (probeStatus string, tokenInvalid bool) {
	switch {
	case !reachable:
		return "unreachable", false
	case status == http.StatusOK:
		return "ok", false
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "invalid", true
	default:
		return "unreachable", false
	}
}

// probeFigmaSeat makes one cheap Tier-1 call against the given file to sniff
// whether the token is seat-limited to the monthly bucket (View/Collab seats
// get ~6 Tier-1 requests/MONTH — feature-dead). Best-effort heuristic: a 429
// whose X-Figma-Rate-Limit-Type header indicates a monthly bucket means
// low_seat; anything ambiguous stays "unknown".
func probeFigmaSeat(ctx context.Context, token, fileKey string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, figmaAPIBase+"/v1/files/"+fileKey+"?depth=1", nil)
	if err != nil {
		return "unknown"
	}
	req.Header.Set("X-Figma-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusOK:
		return "ok"
	case resp.StatusCode == http.StatusTooManyRequests:
		if strings.Contains(strings.ToLower(resp.Header.Get("X-Figma-Rate-Limit-Type")), "month") {
			return "low_seat"
		}
		return "unknown"
	default:
		return "unknown"
	}
}

// PutFigmaCredential saves (or rotates) the workspace's Figma PAT. The token
// is probed against /v1/me before sealing so a typo'd token is rejected with
// 422 instead of silently breaking every agent's Figma access.
func (h *Handler) PutFigmaCredential(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req putFigmaCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	req.ProbeFileKey = strings.TrimSpace(req.ProbeFileKey)
	if req.ProbeFileKey != "" && !probeFileKeyRe.MatchString(req.ProbeFileKey) {
		writeError(w, http.StatusBadRequest, "probe_file_key must match ^[A-Za-z0-9]{10,}$")
		return
	}

	var expiresAt pgtype.Timestamptz
	if s := strings.TrimSpace(req.ExpiresAt); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t, err = time.Parse("2006-01-02", s)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "expires_at must be RFC3339 or YYYY-MM-DD")
			return
		}
		expiresAt = pgtype.Timestamptz{Time: t, Valid: true}
	}

	box, err := figmaCredentialBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "figma credentials are not configured on this server (AGORA_FIGMA_SECRET_KEY unset)")
		return
	}

	status, reachable := probeFigmaToken(r.Context(), token)
	probeStatus, tokenInvalid := classifyFigmaProbe(status, reachable)
	if tokenInvalid {
		writeError(w, http.StatusUnprocessableEntity, "figma_token_invalid: Figma rejected the token (GET /v1/me)")
		return
	}
	seatProbe := "unknown"
	if probeStatus == "ok" && req.ProbeFileKey != "" {
		seatProbe = probeFigmaSeat(r.Context(), token, req.ProbeFileKey)
	}

	sealed, err := box.Seal([]byte(token))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal token")
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}
	last4 := token
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "Figma"
	}
	tokenKind := "pat"
	if strings.HasPrefix(token, "figpat_") {
		tokenKind = "plan_access_token"
	}
	row, err := h.Queries.UpsertFigmaCredential(r.Context(), db.UpsertFigmaCredentialParams{
		WorkspaceID:    wsUUID,
		Label:          label,
		TokenEncrypted: sealed,
		TokenLast4:     last4,
		TokenKind:      tokenKind,
		ExpiresAt:      expiresAt,
		SeatProbe:      seatProbe,
		ProbeStatus:    probeStatus,
		CreatedBy:      creator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save figma credential")
		return
	}
	writeJSON(w, http.StatusOK, figmaCredentialStatusFromRow(row))
}

// GetFigmaCredentialStatus is member-visible (the integrations tab must render
// for non-admins) and never returns token material — token_last4 only.
func (h *Handler) GetFigmaCredentialStatus(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	row, err := h.Queries.GetFigmaCredentialForWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, figmaCredentialStatusResponse{Configured: false})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load figma credential")
		return
	}
	writeJSON(w, http.StatusOK, figmaCredentialStatusFromRow(row))
}

func figmaCredentialStatusFromRow(row db.FigmaCredential) figmaCredentialStatusResponse {
	resp := figmaCredentialStatusResponse{
		Configured:  true,
		Label:       row.Label,
		TokenLast4:  row.TokenLast4,
		TokenKind:   row.TokenKind,
		SeatProbe:   row.SeatProbe,
		ProbeStatus: row.ProbeStatus,
	}
	if row.ExpiresAt.Valid {
		resp.ExpiresAt = row.ExpiresAt.Time.UTC().Format(time.RFC3339)
		resp.ExpiringSoon = time.Until(row.ExpiresAt.Time) < 14*24*time.Hour
	}
	if row.ProbedAt.Valid {
		resp.ProbedAt = row.ProbedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// DeleteFigmaCredential removes the workspace's credential.
func (h *Handler) DeleteFigmaCredential(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.DeleteFigmaCredential(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete figma credential")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "figma credential not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decryptWorkspaceFigmaToken resolves the workspace credential for server-side
// use (claim-time injection, probes). Returns ok=false when there is no
// credential, the secret key is unset, or decryption fails; expired reports
// whether the credential is past expires_at or its last probe flagged it.
func (h *Handler) decryptWorkspaceFigmaToken(ctx context.Context, wsUUID pgtype.UUID) (token string, expired bool, ok bool) {
	row, err := h.Queries.GetFigmaCredentialForWorkspace(ctx, wsUUID)
	if err != nil {
		return "", false, false
	}
	if row.ExpiresAt.Valid && time.Now().After(row.ExpiresAt.Time) {
		return "", true, false
	}
	if row.ProbeStatus == "expired" || row.ProbeStatus == "invalid" {
		return "", true, false
	}
	box, err := figmaCredentialBox()
	if err != nil {
		return "", false, false
	}
	plain, err := box.Open(row.TokenEncrypted)
	if err != nil {
		return "", false, false
	}
	return string(plain), false, true
}
