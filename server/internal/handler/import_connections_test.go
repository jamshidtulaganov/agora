package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/linear"
	"github.com/jamshidtulaganov/agora/server/internal/middleware"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The sealed-credential endpoints (docs/importers-plan.md §3.8).
//
// Every assertion below is a search for one of three failures that have
// happened to real importers: a token echoed back in a response, a token
// stored without ever being checked, and a token stored in the clear because
// the seal key happened to be unset.

// importAPICanaryToken is the plaintext every leak assertion greps for. It
// looks like a Linear personal API key because that is what it stands in for.
const importAPICanaryToken = "lin_api_PR4LEAKCANARY0000000001"

// importAPISealKey installs a deterministic seal key for one test. t.Setenv
// restores the environment afterwards, so a later test still sees an
// unconfigured deployment unless it asks for one.
func importAPISealKey(t *testing.T) {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	t.Setenv(imports.SecretKeyEnv, base64.StdEncoding.EncodeToString(key))
}

// importAPILinearHost is a stand-in Linear GraphQL endpoint serving the one
// query a probe makes. It records what it was sent so the "no Bearer prefix"
// contract can be asserted from the handler side too.
type importAPILinearHost struct {
	srv *httptest.Server

	mu           sync.Mutex
	unauthorized bool
	unreachable  bool
	authHeaders  []string
}

func newImportAPILinearHost(t *testing.T) *importAPILinearHost {
	t.Helper()
	host := &importAPILinearHost{}
	host.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(body, &req)

		host.mu.Lock()
		host.authHeaders = append(host.authHeaders, r.Header.Get("Authorization"))
		unauthorized := host.unauthorized
		unreachable := host.unreachable
		host.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case unreachable:
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"errors":[{"message":"service unavailable"}]}`)
			return
		case unauthorized:
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"errors":[{"message":"Authentication required - not authenticated"}]}`)
			return
		}
		if strings.Contains(req.Query, "teams(") {
			io.WriteString(w, `{"data":{"teams":{"pageInfo":{"hasNextPage":false,"endCursor":""},`+
				`"nodes":[{"id":"team-eng","key":"ENG","name":"Engineering","description":"","icon":null,"archivedAt":null}]}}}`)
			return
		}
		io.WriteString(w, `{"data":{"viewer":{"id":"usr-kim","name":"Kim Ryu","email":"kim@acme.io"},`+
			`"organization":{"id":"org-1","name":"Acme","urlKey":"acme"}}}`)
	}))
	t.Cleanup(host.srv.Close)
	return host
}

func (m *importAPILinearHost) setUnauthorized(v bool) {
	m.mu.Lock()
	m.unauthorized = v
	m.mu.Unlock()
}

func (m *importAPILinearHost) setUnreachable(v bool) {
	m.mu.Lock()
	m.unreachable = v
	m.mu.Unlock()
}

func (m *importAPILinearHost) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.authHeaders)
}

func (m *importAPILinearHost) lastAuthHeader() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.authHeaders) == 0 {
		return ""
	}
	return m.authHeaders[len(m.authHeaders)-1]
}

// importAPIUseLinear points the adapter factory at the stub host. The REAL
// Linear adapter is built — the factory seam replaces the endpoint, not the
// adapter — so these tests exercise the actual GraphQL client, its auth header
// and its error classification.
func importAPIUseLinear(t *testing.T, host *importAPILinearHost) {
	t.Helper()
	prev := newImportAdapter
	newImportAdapter = func(conn db.ImportConnection, token string) (imports.Adapter, error) {
		return linear.NewAdapter(linear.New(linear.Config{APIKey: token, Endpoint: host.srv.URL})), nil
	}
	t.Cleanup(func() { newImportAdapter = prev })
}

// importAPIWorkspace makes an isolated workspace with one owner, so a test
// asserting workspace fencing has a second tenant to be fenced from.
func importAPIWorkspace(t *testing.T, slug string) (workspaceID, ownerID string) {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("import-api-%s@agora.dev", slug)
	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)

	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Import API "+slug, email).Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $2, '', $3) RETURNING id`,
		"Import API "+slug, slug, "IMP").Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		workspaceID, ownerID); err != nil {
		t.Fatalf("create member: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = testPool.Exec(bg, `DELETE FROM "user" WHERE id = $1`, ownerID)
	})
	return workspaceID, ownerID
}

// importAPIRequest builds a workspace-scoped request as the given user. The
// handlers read the workspace from the chi URL param when no middleware has
// put one in the context, which is how every direct-call handler test works.
func importAPIRequest(t *testing.T, method, path, workspaceID, userID string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, path, body)
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	return withURLParam(req, "id", workspaceID)
}

func withExtraURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.RouteContext(req.Context())
	if rctx == nil {
		return withURLParam(req, key, value)
	}
	rctx.URLParams.Add(key, value)
	return req
}

func importAPIConnectionCount(t *testing.T, workspaceID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM import_connection WHERE workspace_id = $1`, workspaceID).Scan(&n); err != nil {
		t.Fatalf("count connections: %v", err)
	}
	return n
}

// TestCreateImportConnection_SealsTokenAndNeverEchoesIt is the leak test: the
// plaintext must not appear in the response, and the column must not hold it.
func TestCreateImportConnection_SealsTokenAndNeverEchoesIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	importAPISealKey(t)
	host := newImportAPILinearHost(t)
	importAPIUseLinear(t, host)
	wsID, userID := importAPIWorkspace(t, "import-api-seal")

	req := importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", wsID, userID,
		map[string]any{"source": "linear", "secret": importAPICanaryToken})
	rec := httptest.NewRecorder()
	testHandler.CreateImportConnection(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create connection: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), importAPICanaryToken) {
		t.Fatalf("response echoed the token: %s", rec.Body.String())
	}
	var created struct {
		ID           string `json:"id"`
		Source       string `json:"source"`
		Label        string `json:"label"`
		AccountEmail string `json:"account_email"`
		ProbeStatus  string `json:"probe_status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.ProbeStatus != imports.ProbeOK {
		t.Errorf("probe_status = %q, want ok", created.ProbeStatus)
	}
	if created.AccountEmail != "kim@acme.io" {
		t.Errorf("account_email = %q, want the probed account", created.AccountEmail)
	}
	if !strings.Contains(created.Label, "acme") {
		t.Errorf("label = %q, want it to name the source workspace", created.Label)
	}
	// Linear wants the key verbatim — a Bearer prefix is a 401.
	if got := host.lastAuthHeader(); got != importAPICanaryToken {
		t.Errorf("Authorization = %q, want the raw key with no Bearer prefix", got)
	}

	// At rest: sealed, and openable back to the original.
	var sealed []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT secret_encrypted FROM import_connection WHERE id = $1`, created.ID).Scan(&sealed); err != nil {
		t.Fatalf("read stored secret: %v", err)
	}
	if strings.Contains(string(sealed), importAPICanaryToken) {
		t.Fatal("secret_encrypted holds the plaintext token")
	}
	plain, err := imports.OpenSecret(sealed)
	if err != nil {
		t.Fatalf("OpenSecret: %v", err)
	}
	if plain != importAPICanaryToken {
		t.Errorf("round-tripped secret = %q, want the original", plain)
	}

	// The listing is the other place a token could escape.
	listReq := importAPIRequest(t, http.MethodGet, "/api/workspaces/"+wsID+"/import/connections", wsID, userID, nil)
	listRec := httptest.NewRecorder()
	testHandler.ListImportConnections(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list connections: want 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), importAPICanaryToken) {
		t.Fatalf("listing echoed the token: %s", listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), created.ID) {
		t.Errorf("listing omitted the connection it should contain: %s", listRec.Body.String())
	}
}

// TestCreateImportConnection_ProbeFailureStoresNothing — a key the source
// rejects never reaches the table. The whole point of probing at save time is
// that the failure happens here, not at row 4000.
func TestCreateImportConnection_ProbeFailureStoresNothing(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	importAPISealKey(t)
	host := newImportAPILinearHost(t)
	host.setUnauthorized(true)
	importAPIUseLinear(t, host)
	wsID, userID := importAPIWorkspace(t, "import-api-badkey")

	req := importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", wsID, userID,
		map[string]any{"source": "linear", "secret": importAPICanaryToken})
	rec := httptest.NewRecorder()
	testHandler.CreateImportConnection(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid token: want 422, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), importAPICanaryToken) {
		t.Fatalf("error response echoed the token: %s", rec.Body.String())
	}
	if n := importAPIConnectionCount(t, wsID); n != 0 {
		t.Fatalf("stored %d connections for a rejected token, want 0", n)
	}

	// An unreachable source is a different verdict and also stores nothing.
	host.setUnauthorized(false)
	host.setUnreachable(true)
	rec = httptest.NewRecorder()
	testHandler.CreateImportConnection(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", wsID, userID,
			map[string]any{"source": "linear", "secret": importAPICanaryToken}))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unreachable source: want 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := importAPIConnectionCount(t, wsID); n != 0 {
		t.Fatalf("stored %d connections for an unreachable source, want 0", n)
	}
}

// TestImportConnections_SealKeyUnsetIs503 — fail closed. Without
// AGORA_IMPORT_SECRET_KEY there is no degraded mode that writes plaintext.
func TestImportConnections_SealKeyUnsetIs503(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv(imports.SecretKeyEnv, "")
	host := newImportAPILinearHost(t)
	importAPIUseLinear(t, host)
	wsID, userID := importAPIWorkspace(t, "import-api-nokey")

	rec := httptest.NewRecorder()
	testHandler.CreateImportConnection(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", wsID, userID,
			map[string]any{"source": "linear", "secret": importAPICanaryToken}))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unset seal key: want 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := importAPIConnectionCount(t, wsID); n != 0 {
		t.Fatalf("stored %d connections with no seal key, want 0", n)
	}
	if host.calls() != 0 {
		t.Errorf("probed the source before checking the seal key (%d calls)", host.calls())
	}
}

// TestProbeImportConnection_RecordsTheVerdict — a token that goes bad later
// turns into a visible red row, not a surprise at the next import.
func TestProbeImportConnection_RecordsTheVerdict(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	importAPISealKey(t)
	host := newImportAPILinearHost(t)
	importAPIUseLinear(t, host)
	wsID, userID := importAPIWorkspace(t, "import-api-probe")
	connID := importAPICreateConnection(t, wsID, userID)

	host.setUnauthorized(true)
	rec := httptest.NewRecorder()
	req := importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections/"+connID+"/probe", wsID, userID, nil)
	testHandler.ProbeImportConnection(rec, withExtraURLParam(req, "cid", connID))

	if rec.Code != http.StatusOK {
		t.Fatalf("probe: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Connection importConnectionResponse `json:"connection"`
		Probe      map[string]string        `json:"probe"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	if body.Probe["status"] != imports.ProbeInvalid {
		t.Errorf("probe status = %q, want invalid", body.Probe["status"])
	}
	if body.Connection.ProbeStatus != imports.ProbeInvalid {
		t.Errorf("stored probe_status = %q, want invalid", body.Connection.ProbeStatus)
	}
	if body.Connection.ProbedAt == "" {
		t.Error("probed_at was not recorded")
	}
	if strings.Contains(rec.Body.String(), importAPICanaryToken) {
		t.Fatalf("probe response echoed the token: %s", rec.Body.String())
	}
}

// importAPICreateConnection stores one probed connection through the endpoint
// and returns its id.
func importAPICreateConnection(t *testing.T, workspaceID, userID string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.CreateImportConnection(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID+"/import/connections", workspaceID, userID,
			map[string]any{"source": "linear", "secret": importAPICanaryToken}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create connection: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return created.ID
}

// TestImportConnections_WorkspaceFencing — one tenant's connection is
// invisible and unusable from another, on every verb.
func TestImportConnections_WorkspaceFencing(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	importAPISealKey(t)
	host := newImportAPILinearHost(t)
	importAPIUseLinear(t, host)

	wsA, userA := importAPIWorkspace(t, "import-api-fence-a")
	wsB, userB := importAPIWorkspace(t, "import-api-fence-b")
	connID := importAPICreateConnection(t, wsA, userA)

	listRec := httptest.NewRecorder()
	testHandler.ListImportConnections(listRec,
		importAPIRequest(t, http.MethodGet, "/api/workspaces/"+wsB+"/import/connections", wsB, userB, nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list from workspace B: want 200, got %d", listRec.Code)
	}
	if strings.Contains(listRec.Body.String(), connID) {
		t.Fatalf("workspace B can see workspace A's connection: %s", listRec.Body.String())
	}

	probeRec := httptest.NewRecorder()
	probeReq := importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsB+"/import/connections/"+connID+"/probe", wsB, userB, nil)
	testHandler.ProbeImportConnection(probeRec, withExtraURLParam(probeReq, "cid", connID))
	if probeRec.Code != http.StatusNotFound {
		t.Errorf("probe across workspaces: want 404, got %d: %s", probeRec.Code, probeRec.Body.String())
	}

	delRec := httptest.NewRecorder()
	delReq := importAPIRequest(t, http.MethodDelete, "/api/workspaces/"+wsB+"/import/connections/"+connID, wsB, userB, nil)
	testHandler.DeleteImportConnection(delRec, withExtraURLParam(delReq, "cid", connID))
	if delRec.Code != http.StatusNotFound {
		t.Errorf("delete across workspaces: want 404, got %d: %s", delRec.Code, delRec.Body.String())
	}
	if n := importAPIConnectionCount(t, wsA); n != 1 {
		t.Errorf("workspace A lost its connection to a cross-tenant delete (count=%d)", n)
	}

	// The owner of A can delete it, which also proves the 404 above was the
	// tenant gate and not a broken route.
	okRec := httptest.NewRecorder()
	okReq := importAPIRequest(t, http.MethodDelete, "/api/workspaces/"+wsA+"/import/connections/"+connID, wsA, userA, nil)
	testHandler.DeleteImportConnection(okRec, withExtraURLParam(okReq, "cid", connID))
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("owner delete: want 204, got %d: %s", okRec.Code, okRec.Body.String())
	}
}

// TestImportRoutes_RoleAndActorGating mounts the production middleware so the
// gate is proved where it actually lives: the router. A plain member must not
// be able to store a credential, and a machine actor must not be able to start
// an import on a human's behalf.
func TestImportRoutes_RoleAndActorGating(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	importAPISealKey(t)
	host := newImportAPILinearHost(t)
	importAPIUseLinear(t, host)
	ctx := context.Background()

	wsID, adminID := importAPIWorkspace(t, "import-api-roles")
	var memberID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Import API member", "import-api-roles-member@agora.dev").Scan(&memberID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, wsID, memberID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, memberID)
	})

	// Mirrors the wiring in server/cmd/server/router.go.
	router := chi.NewRouter()
	router.Route("/api/workspaces/{id}/import", func(r chi.Router) {
		r.Use(middleware.RequireWorkspaceRoleFromURL(testHandler.Queries, "id", "owner", "admin"))
		r.Get("/connections", testHandler.ListImportConnections)
		r.With(RequireHumanActor).Post("/connections", testHandler.CreateImportConnection)
		r.With(RequireHumanActor).Post("/dry-run", testHandler.DryRunImport)
	})

	exercise := func(t *testing.T, method, path, userID, actorSource string, body string) int {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", userID)
		if actorSource != "" {
			req.Header.Set("X-Actor-Source", actorSource)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	payload := `{"source":"linear","secret":"` + importAPICanaryToken + `"}`

	if code := exercise(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", memberID, "", payload); code != http.StatusForbidden {
		t.Errorf("member POST connections: want 403, got %d", code)
	}
	if code := exercise(t, http.MethodGet, "/api/workspaces/"+wsID+"/import/connections", memberID, "", ""); code != http.StatusForbidden {
		t.Errorf("member GET connections: want 403, got %d", code)
	}
	if code := exercise(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", adminID, "task_token", payload); code != http.StatusForbidden {
		t.Errorf("task-token actor POST connections: want 403, got %d", code)
	}
	if code := exercise(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/dry-run", adminID, "cloud_pat", `{"connection_id":"x"}`); code != http.StatusForbidden {
		t.Errorf("cloud-pat actor POST dry-run: want 403, got %d", code)
	}
	if n := importAPIConnectionCount(t, wsID); n != 0 {
		t.Fatalf("a refused request still stored %d connections", n)
	}
	if code := exercise(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", adminID, "", payload); code != http.StatusCreated {
		t.Errorf("owner POST connections: want 201, got %d", code)
	}
}

// TestImportEndpoints_DisabledIs503 — the kill switch answers 503 everywhere
// rather than 404, so an operator can tell "off here" from "not built".
func TestImportEndpoints_DisabledIs503(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	importAPISealKey(t)
	t.Setenv(cfgImportEnabled, "false")
	wsID, userID := importAPIWorkspace(t, "import-api-off")

	rec := httptest.NewRecorder()
	testHandler.ListImportConnections(rec,
		importAPIRequest(t, http.MethodGet, "/api/workspaces/"+wsID+"/import/connections", wsID, userID, nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("list with the feature off: want 503, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	testHandler.DryRunImport(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/dry-run", wsID, userID,
			map[string]any{"connection_id": wsID}))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("dry-run with the feature off: want 503, got %d", rec.Code)
	}
}

// TestCreateImportConnection_RefusesALinearBaseURL — base_url is Jira's site
// URL. Honouring one for Linear would let an admin aim a customer's API key at
// a host they control.
func TestCreateImportConnection_RefusesALinearBaseURL(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	importAPISealKey(t)
	host := newImportAPILinearHost(t)
	importAPIUseLinear(t, host)
	wsID, userID := importAPIWorkspace(t, "import-api-baseurl")

	rec := httptest.NewRecorder()
	testHandler.CreateImportConnection(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/connections", wsID, userID,
			map[string]any{"source": "linear", "secret": importAPICanaryToken, "base_url": "https://attacker.example"}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("linear base_url: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if host.calls() != 0 {
		t.Errorf("the token was sent somewhere before the request was refused (%d calls)", host.calls())
	}
	if n := importAPIConnectionCount(t, wsID); n != 0 {
		t.Fatalf("stored %d connections for a refused request, want 0", n)
	}
}
