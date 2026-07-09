package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeConfigList(t *testing.T, body []byte) []instanceConfigEntry {
	t.Helper()
	var resp struct {
		Configs []instanceConfigEntry `json:"configs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return resp.Configs
}

func TestGetInstanceConfig(t *testing.T) {
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM instance_config`)
	})
	w := httptest.NewRecorder()
	testHandler.GetInstanceConfig(w, newRequest("GET", "/api/instance-config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", w.Code, w.Body.String())
	}
	entries := decodeConfigList(t, w.Body.Bytes())
	if len(entries) == 0 {
		t.Fatal("expected config entries")
	}
	var sawFlag, sawSecret bool
	for _, e := range entries {
		if e.Key == "AGORA_AUTO_QA_ENABLED" {
			sawFlag = true
			if e.Kind != "bool" || !e.Editable {
				t.Errorf("AGORA_AUTO_QA_ENABLED wrong: %+v", e)
			}
		}
		if e.Kind == "secret" {
			sawSecret = true
			if e.Editable {
				t.Errorf("secret %s must not be editable", e.Key)
			}
			if e.Value != "" {
				t.Errorf("secret %s value must never be exposed, got %q", e.Key, e.Value)
			}
		}
	}
	if !sawFlag || !sawSecret {
		t.Errorf("expected both a flag and a secret entry (flag=%v secret=%v)", sawFlag, sawSecret)
	}
}

func TestSetAndResetInstanceConfig(t *testing.T) {
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM instance_config`)
	})

	// Set a bool flag override.
	req := withURLParam(newRequest("PUT", "/api/instance-config/AGORA_AUTO_QA_ENABLED", map[string]any{"value": "true"}), "key", "AGORA_AUTO_QA_ENABLED")
	w := httptest.NewRecorder()
	testHandler.SetInstanceConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}
	var row string
	if err := testPool.QueryRow(context.Background(),
		`SELECT value FROM instance_config WHERE key=$1`, "AGORA_AUTO_QA_ENABLED").Scan(&row); err != nil {
		t.Fatalf("override not persisted: %v", err)
	}
	if row != "true" {
		t.Errorf("stored value = %q, want true", row)
	}

	// Reset removes the override.
	req = withURLParam(newRequest("DELETE", "/api/instance-config/AGORA_AUTO_QA_ENABLED", nil), "key", "AGORA_AUTO_QA_ENABLED")
	w = httptest.NewRecorder()
	testHandler.ResetInstanceConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body.String())
	}
	var n int
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM instance_config WHERE key=$1`, "AGORA_AUTO_QA_ENABLED").Scan(&n)
	if n != 0 {
		t.Errorf("override should be deleted, found %d rows", n)
	}
}

func TestSetInstanceConfigRejectsSecretAndUnknownAndBadValue(t *testing.T) {
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM instance_config`) })

	// Secret key → 403.
	req := withURLParam(newRequest("PUT", "/api/instance-config/JWT_SECRET", map[string]any{"value": "x"}), "key", "JWT_SECRET")
	w := httptest.NewRecorder()
	testHandler.SetInstanceConfig(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("secret key should be 403, got %d %s", w.Code, w.Body.String())
	}

	// Unknown key → 400.
	req = withURLParam(newRequest("PUT", "/api/instance-config/NOT_A_REAL_KEY", map[string]any{"value": "x"}), "key", "NOT_A_REAL_KEY")
	w = httptest.NewRecorder()
	testHandler.SetInstanceConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown key should be 400, got %d", w.Code)
	}

	// Bad int value → 400.
	req = withURLParam(newRequest("PUT", "/api/instance-config/AGORA_QA_WATCHDOG_WINDOW_HOURS", map[string]any{"value": "abc"}), "key", "AGORA_QA_WATCHDOG_WINDOW_HOURS")
	w = httptest.NewRecorder()
	testHandler.SetInstanceConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad int should be 400, got %d", w.Code)
	}
}
