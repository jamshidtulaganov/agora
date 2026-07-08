package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The self-scoped import must refuse a caller who has not linked their Bitrix
// account — without a linked responsible id we cannot scope the import to them,
// and importing anything else would leak other users' tasks. The refusal is a
// 412 with reason "bitrix_not_linked" and returns BEFORE any Bitrix REST call,
// so this test needs only a DB (to resolve "no link") and the endpoints gate.
func TestImportMyBitrixTasks_UnlinkedCallerGets412(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// The endpoints gate is bitrixEndpointsEnabled() == (BITRIX_WEBHOOK_URL != "").
	t.Setenv("BITRIX_WEBHOOK_URL", "https://example.bitrix24.test/rest/1/testtoken/")

	// A fresh, unlinked user id — bitrixIDByUserID returns "" (no rows) for it.
	req := httptest.NewRequest(http.MethodPost, "/api/bitrix/import/mine", nil)
	req.Header.Set("X-User-ID", uuid.NewString())
	rec := httptest.NewRecorder()

	testHandler.ImportMyBitrixTasks(rec, req)

	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("unlinked caller: want 412, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Reason string `json:"reason"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Reason != "bitrix_not_linked" {
		t.Fatalf("want reason bitrix_not_linked, got %q", body.Reason)
	}
	if body.Error == "" {
		t.Fatal("expected an actionable error message telling the user to link Bitrix first")
	}
}

// The operator bulk-import paths (arbitrary selectors / whole-group sync) are
// the cross-user hole; they must be OFF unless an operator deliberately enables
// a backfill. Default-off is the security property, so pin it.
func TestBitrixBulkImportEnabled_DefaultOffAndTruthy(t *testing.T) {
	t.Setenv("AGORA_BITRIX_BULK_IMPORT", "")
	if bitrixBulkImportEnabled() {
		t.Fatal("bulk import must be OFF by default — an unset flag would reopen the cross-user hole")
	}
	for _, off := range []string{"0", "false", "no", "off", "nope"} {
		t.Setenv("AGORA_BITRIX_BULK_IMPORT", off)
		if bitrixBulkImportEnabled() {
			t.Fatalf("value %q must not enable bulk import", off)
		}
	}
	for _, on := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv("AGORA_BITRIX_BULK_IMPORT", on)
		if !bitrixBulkImportEnabled() {
			t.Fatalf("value %q should enable bulk import (backfill)", on)
		}
	}
}

// Missing auth (no X-User-ID) is a 401, never a Bitrix call.
func TestImportMyBitrixTasks_Unauthenticated(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("BITRIX_WEBHOOK_URL", "https://example.bitrix24.test/rest/1/testtoken/")

	req := httptest.NewRequest(http.MethodPost, "/api/bitrix/import/mine", nil)
	rec := httptest.NewRecorder()
	testHandler.ImportMyBitrixTasks(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth: want 401, got %d", rec.Code)
	}
}

// The own-only webhook/poller tightening is OFF by default (phased: the live
// sync keeps its "import unassigned" behavior until a workspace opts in); only an
// explicit truthy value enforces the strict per-user model.
func TestBitrixImportOwnOnly_DefaultOff(t *testing.T) {
	t.Setenv("AGORA_BITRIX_IMPORT_OWN_ONLY", "")
	if bitrixImportOwnOnly() {
		t.Fatal("own-only must default OFF — unset must preserve the legacy import-unassigned behavior")
	}
	for _, off := range []string{"0", "false", "no", "off", "nope"} {
		t.Setenv("AGORA_BITRIX_IMPORT_OWN_ONLY", off)
		if bitrixImportOwnOnly() {
			t.Fatalf("value %q should keep own-only OFF", off)
		}
	}
	for _, on := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv("AGORA_BITRIX_IMPORT_OWN_ONLY", on)
		if !bitrixImportOwnOnly() {
			t.Fatalf("value %q should enable own-only enforcement", on)
		}
	}
}
