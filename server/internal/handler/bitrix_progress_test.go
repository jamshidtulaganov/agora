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
	}, nil)
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
	}, nil)
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

// TestBitrixImportProgressCancelStopsRun covers the operator cancel path: the
// run's context is cancelled, the snapshot flips to cancelled/not-running, and
// the partial counters are preserved (cancelling stops the remaining queue, it
// does not roll back what already imported).
func TestBitrixImportProgressCancelStopsRun(t *testing.T) {
	cancelled := false
	runID := bitrixImportProgressStart(3, []bitrixImportProgressSelector{
		{Kind: "group", ID: "153", TaskIDs: map[string]bool{"a": true, "b": true, "c": true}},
	}, func() { cancelled = true })
	bitrixImportProgressInc(runID, "a")

	ok, synced, total := bitrixImportProgressCancel()
	if !ok {
		t.Fatal("cancel reported nothing to stop while a run was live")
	}
	if !cancelled {
		t.Error("run context was not cancelled, so the goroutine would keep importing")
	}
	if synced != 1 || total != 3 {
		t.Errorf("cancel reported %d/%d, want 1/3", synced, total)
	}

	bitrixImportProgressState.Lock()
	running, wasCancelled, keptSynced := bitrixImportProgressState.Running,
		bitrixImportProgressState.Cancelled, bitrixImportProgressState.Synced
	itemRunning := bitrixImportProgressState.Items[0].Running
	bitrixImportProgressState.Unlock()
	if running || itemRunning {
		t.Error("progress still reports running after cancel")
	}
	if !wasCancelled {
		t.Error("cancelled flag not set — UI could not distinguish a cancel from a crash")
	}
	if keptSynced != 1 {
		t.Errorf("synced = %d, want the partial count 1 preserved", keptSynced)
	}

	// Cancelling again is a no-op and must say so rather than claim success.
	if ok, _, _ := bitrixImportProgressCancel(); ok {
		t.Error("second cancel claimed to stop a run that was already stopped")
	}
}

// TestBitrixImportProgressStartSupersedesPreviousRun: starting a second import
// must cancel the first, otherwise its goroutine keeps importing with no handle
// left to stop it (the global snapshot is the only one).
func TestBitrixImportProgressStartSupersedesPreviousRun(t *testing.T) {
	firstCancelled := false
	bitrixImportProgressStart(2, []bitrixImportProgressSelector{
		{Kind: "group", ID: "149", TaskIDs: map[string]bool{"a": true, "b": true}},
	}, func() { firstCancelled = true })

	bitrixImportProgressStart(1, []bitrixImportProgressSelector{
		{Kind: "group", ID: "151", TaskIDs: map[string]bool{"z": true}},
	}, nil)

	if !firstCancelled {
		t.Error("previous run was orphaned instead of cancelled")
	}
	bitrixImportProgressState.Lock()
	if bitrixImportProgressState.Cancelled {
		t.Error("a superseded run must not mark the NEW run as cancelled")
	}
	bitrixImportProgressState.Unlock()
}
