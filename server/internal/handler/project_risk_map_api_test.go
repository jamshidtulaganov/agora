package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The risk-map write endpoint (docs/orchestration-upgrade-plan.md §A1.2).
//
// Every assertion here is a property the safety control depends on:
//
//   - a malformed map is REFUSED at the door, not silently dropped at read time
//     (a dropped map leaves an admin believing a control is in force);
//   - the write is KEY-SCOPED — sibling settings survive it;
//   - only an owner/admin may author it, because it decides what an agent may
//     merge without a human;
//   - the workspace fence holds on read and write.

func riskMapTestProject(t *testing.T, settings string) string {
	t.Helper()
	if settings == "" {
		settings = "{}"
	}
	var projectID string
	if err := testPool.QueryRow(t.Context(),
		`INSERT INTO project (workspace_id, title, settings)
		 VALUES ($1::uuid, 'Risk Map Test', $2::jsonb) RETURNING id::text`,
		testWorkspaceID, settings).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1::uuid`, projectID)
	})
	return projectID
}

func putRiskMap(t *testing.T, projectID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPut, "/api/projects/"+projectID+"/risk-map", body), "id", projectID)
	w := httptest.NewRecorder()
	testHandler.SetProjectRiskMap(w, req)
	return w
}

func TestSetProjectRiskMapStoresAndNormalizes(t *testing.T) {
	// A sibling key that must survive the write — the risk-map file explicitly
	// warns about clobbering siblings, and UpdateProject NULLs five unguarded
	// columns when handed a partial struct.
	projectID := riskMapTestProject(t, `{"qa_manifest": {"base_url": "https://example.test"}}`)

	w := putRiskMap(t, projectID, map[string]any{
		"risk_map": []map[string]any{
			{"module": "billing", "tier": "CRITICAL", "paths": []string{"./protected/modules/pay/**", "/protected/controllers/Kassa*", "protected/modules/pay/**"}, "owner": " Davron "},
			{"module": "reports", "paths": []string{"protected/views/report/"}},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp projectRiskMapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Configured || len(resp.RiskMap) != 2 {
		t.Fatalf("expected 2 configured entries, got %+v", resp)
	}
	if resp.DefaultTier != riskTierGuarded {
		t.Errorf("the unknown-path default must be guarded, got %q", resp.DefaultTier)
	}
	billing := resp.RiskMap[0]
	if billing.Tier != riskTierCritical {
		t.Errorf("tier must be normalized to lower case, got %q", billing.Tier)
	}
	if len(billing.Paths) != 2 {
		t.Errorf("a duplicate glob must be dropped after normalization, got %v", billing.Paths)
	}
	for _, p := range billing.Paths {
		if p[0] == '.' || p[0] == '/' {
			t.Errorf("path %q was not normalized", p)
		}
	}
	if billing.Owner != "Davron" {
		t.Errorf("owner must be trimmed, got %q", billing.Owner)
	}
	// An entry with no tier is GUARDED, never safe.
	if resp.RiskMap[1].Tier != riskTierGuarded {
		t.Errorf("a tier-less entry must store as guarded, got %q", resp.RiskMap[1].Tier)
	}

	// The sibling settings key survived the key-scoped jsonb_set.
	var manifest *string
	if err := testPool.QueryRow(t.Context(),
		`SELECT settings->'qa_manifest'->>'base_url' FROM project WHERE id = $1::uuid`, projectID).Scan(&manifest); err != nil {
		t.Fatalf("read siblings: %v", err)
	}
	if manifest == nil || *manifest != "https://example.test" {
		t.Fatalf("the risk-map write clobbered a sibling settings key: %v", manifest)
	}

	// And the stored map is what the resolver will later read back.
	readReq := withURLParam(newRequest(http.MethodGet, "/api/projects/"+projectID+"/risk-map", nil), "id", projectID)
	readW := httptest.NewRecorder()
	testHandler.GetProjectRiskMap(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", readW.Code, readW.Body.String())
	}
	var readBack projectRiskMapResponse
	json.Unmarshal(readW.Body.Bytes(), &readBack)
	if len(readBack.RiskMap) != 2 || !readBack.Configured {
		t.Fatalf("GET did not return the stored map: %+v", readBack)
	}
}

func TestGetProjectRiskMapUnconfigured(t *testing.T) {
	projectID := riskMapTestProject(t, "")
	req := withURLParam(newRequest(http.MethodGet, "/api/projects/"+projectID+"/risk-map", nil), "id", projectID)
	w := httptest.NewRecorder()
	testHandler.GetProjectRiskMap(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for an untiered project, got %d", w.Code)
	}
	var resp projectRiskMapResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Configured {
		t.Error("a project with no map must report configured=false")
	}
	if resp.RiskMap == nil {
		t.Error("the empty map must serialize as [], not null")
	}
}

func TestSetProjectRiskMapValidation(t *testing.T) {
	projectID := riskMapTestProject(t, "")
	tooMany := make([]map[string]any, 0, riskMapMaxEntries+1)
	for i := 0; i <= riskMapMaxEntries; i++ {
		tooMany = append(tooMany, map[string]any{"module": "m" + string(rune('a'+i%26)) + string(rune('a'+i/26)), "tier": "safe", "paths": []string{"x"}})
	}

	cases := []struct {
		name string
		body any
		want string // a fragment the 400 must name
	}{
		{"module is required", map[string]any{"risk_map": []map[string]any{{"tier": "safe", "paths": []string{"a"}}}}, "module is required"},
		{"paths are required", map[string]any{"risk_map": []map[string]any{{"module": "billing", "tier": "critical"}}}, "path glob is required"},
		{"an unknown tier is refused, not coerced", map[string]any{"risk_map": []map[string]any{{"module": "billing", "tier": "medium", "paths": []string{"a"}}}}, "tier must be"},
		{"duplicate modules", map[string]any{"risk_map": []map[string]any{
			{"module": "Billing", "tier": "safe", "paths": []string{"a"}},
			{"module": "billing", "tier": "critical", "paths": []string{"b"}},
		}}, "duplicate module"},
		{"a glob that compiles to nothing is refused", map[string]any{"risk_map": []map[string]any{{"module": "billing", "tier": "critical", "paths": []string{"protected/[unterminated"}}}}, "malformed path glob"},
		{"an empty glob is refused", map[string]any{"risk_map": []map[string]any{{"module": "billing", "tier": "critical", "paths": []string{"   "}}}}, "empty path glob"},
		{"a file inventory is refused", map[string]any{"risk_map": tooMany}, "tiering of modules"},
	}
	for _, c := range cases {
		w := putRiskMap(t, projectID, c.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d: %s", c.name, w.Code, w.Body.String())
			continue
		}
		if !jsonErrorContains(w.Body.Bytes(), c.want) {
			t.Errorf("%s: 400 did not name the problem (%q): %s", c.name, c.want, w.Body.String())
		}
	}

	// A malformed map must NOT have landed: the project is still untiered.
	var raw *string
	testPool.QueryRow(t.Context(), `SELECT settings->>'risk_map' FROM project WHERE id = $1::uuid`, projectID).Scan(&raw)
	if raw != nil {
		t.Fatalf("a refused write still touched settings.risk_map: %v", *raw)
	}
}

// An empty array is a legitimate "remove the tiering" — the project returns to
// having no opinion rather than to a half-configured one.
func TestSetProjectRiskMapEmptyClearsTiering(t *testing.T) {
	projectID := riskMapTestProject(t, `{"risk_map": [{"module":"billing","tier":"critical","paths":["pay/**"]}]}`)
	w := putRiskMap(t, projectID, map[string]any{"risk_map": []map[string]any{}})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp projectRiskMapResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Configured {
		t.Error("an emptied map must report configured=false")
	}
}

// Authoring the tiering decides what may merge without a human, so a plain
// member may READ it and may not WRITE it.
func TestSetProjectRiskMapRequiresOwnerOrAdmin(t *testing.T) {
	projectID := riskMapTestProject(t, "")
	ctx := t.Context()
	if _, err := testPool.Exec(ctx,
		`UPDATE member SET role = 'member' WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		testWorkspaceID, testUserID); err != nil {
		t.Fatalf("demote: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE member SET role = 'owner' WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
			testWorkspaceID, testUserID)
	})

	w := putRiskMap(t, projectID, map[string]any{"risk_map": []map[string]any{{"module": "billing", "tier": "critical", "paths": []string{"pay/**"}}}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("a plain member must not author the risk map: got %d: %s", w.Code, w.Body.String())
	}

	readReq := withURLParam(newRequest(http.MethodGet, "/api/projects/"+projectID+"/risk-map", nil), "id", projectID)
	readW := httptest.NewRecorder()
	testHandler.GetProjectRiskMap(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("a member must be able to read the policy their changes are judged by: got %d", readW.Code)
	}
}

// The workspace fence: a project in another tenant does not exist here, on
// either verb.
func TestProjectRiskMapWorkspaceFence(t *testing.T) {
	ctx := t.Context()
	var otherWS, otherProject string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix)
		 VALUES ('Risk Fence', 'risk-fence-'||substr(gen_random_uuid()::text,1,8), '', 'RSK')
		 RETURNING id::text`).Scan(&otherWS); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, otherWS) })
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title) VALUES ($1::uuid, 'Foreign') RETURNING id::text`, otherWS).Scan(&otherProject); err != nil {
		t.Fatalf("create foreign project: %v", err)
	}

	for _, tc := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"read", func() *httptest.ResponseRecorder {
			req := withURLParam(newRequest(http.MethodGet, "/api/projects/"+otherProject+"/risk-map", nil), "id", otherProject)
			w := httptest.NewRecorder()
			testHandler.GetProjectRiskMap(w, req)
			return w
		}},
		{"write", func() *httptest.ResponseRecorder {
			return putRiskMap(t, otherProject, map[string]any{"risk_map": []map[string]any{{"module": "x", "tier": "safe", "paths": []string{"a"}}}})
		}},
	} {
		if w := tc.run(); w.Code != http.StatusNotFound {
			t.Errorf("%s across the workspace fence must 404, got %d: %s", tc.name, w.Code, w.Body.String())
		}
	}
}

// jsonErrorContains asserts the refusal NAMES the problem. A 400 that says
// only "invalid" is a support ticket; the message is part of the contract.
func jsonErrorContains(body []byte, want string) bool {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		return strings.Contains(e.Error, want)
	}
	return strings.Contains(string(body), want)
}
