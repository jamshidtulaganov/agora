package handler

import (
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
)

func TestBitrixUserAllowedEmailHonorsConfiguredBoundary(t *testing.T) {
	t.Setenv("BITRIX_SYNC_USER_EMAILS", "owner@example.com, reviewer@example.com")

	if !bitrixUserAllowedEmail(&bitrix.User{Email: " Reviewer@Example.com "}) {
		t.Fatal("configured email should be allowed case-insensitively")
	}
	if bitrixUserAllowedEmail(&bitrix.User{Email: "someone-else@example.com"}) {
		t.Fatal("unconfigured portal user must not appear importable")
	}
	if bitrixUserAllowedEmail(nil) {
		t.Fatal("nil portal user must fail closed while an allowlist exists")
	}
}

func TestBitrixImportProgressTracksSelectorsAndRejectsStaleRuns(t *testing.T) {
	runID := bitrixImportProgressStart(3, []bitrixImportProgressSelector{
		{Kind: "user", ID: "191", TaskIDs: map[string]bool{"a": true, "b": true}},
		{Kind: "user", ID: "207", TaskIDs: map[string]bool{"c": true}},
	})
	bitrixImportProgressInc(runID, "a")

	bitrixImportProgressState.Lock()
	if got := bitrixImportProgressState.Synced; got != 1 {
		t.Errorf("global synced = %d, want 1", got)
	}
	if got := bitrixImportProgressState.Items[0].Synced; got != 1 {
		t.Errorf("first user synced = %d, want 1", got)
	}
	if got := bitrixImportProgressState.Items[1].Synced; got != 0 {
		t.Errorf("second user synced = %d, want 0", got)
	}
	bitrixImportProgressState.Unlock()

	newRunID := bitrixImportProgressStart(1, []bitrixImportProgressSelector{
		{Kind: "user", ID: "207", TaskIDs: map[string]bool{"z": true}},
	})
	bitrixImportProgressInc(runID, "b")
	bitrixImportProgressFinish(runID)

	bitrixImportProgressState.Lock()
	if bitrixImportProgressState.Synced != 0 || !bitrixImportProgressState.Running {
		t.Errorf("stale run mutated current progress: synced=%d running=%v",
			bitrixImportProgressState.Synced, bitrixImportProgressState.Running)
	}
	bitrixImportProgressState.Unlock()

	bitrixImportProgressInc(newRunID, "z")
	bitrixImportProgressFinish(newRunID)
	bitrixImportProgressState.Lock()
	if bitrixImportProgressState.Synced != 1 || bitrixImportProgressState.Running {
		t.Errorf("current run did not finish cleanly: synced=%d running=%v",
			bitrixImportProgressState.Synced, bitrixImportProgressState.Running)
	}
	bitrixImportProgressState.Unlock()
}
