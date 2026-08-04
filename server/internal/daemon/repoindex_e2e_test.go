package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/repoindex"
)

// findTreatmentTaskID returns a task ID that lands in the given arm, so the
// end-to-end test can exercise both sides without depending on a fixed UUID
// hashing a particular way.
func findArmTaskID(t *testing.T, arm int) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		id := "task-" + string(rune('a'+i%26)) + strings.Repeat("x", i%7) + itoa(i)
		if packArm(id) == arm {
			return id
		}
	}
	t.Fatalf("no task id found for arm %d", arm)
	return ""
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func writeTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"internal/issue/status.go": "package issue\n\n// UpdateIssueStatus transitions an issue between statuses.\nfunc UpdateIssueStatus(id, status string) error { return nil }\n",
		"internal/billing/card.go": "package billing\n\nfunc ChargeCard() {}\n",
		".env":                     "SECRET_TOKEN=sk_live_must_never_appear\n",
	}
	for rel, body := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestBuildRepoPackTreatmentInjects is the end-to-end contract: a treatment
// task on a code issue gets a framed, secret-free pack naming the right file.
func TestBuildRepoPackTreatmentInjects(t *testing.T) {
	d := &Daemon{}
	task := Task{
		ID:         findArmTaskID(t, 1),
		IssueID:    "MUL-1",
		ThreadName: "Update issue status transition",
		IssueBody:  "UpdateIssueStatus should validate the target status",
	}
	pack, stats := d.buildRepoPack(context.Background(), task, writeTestRepo(t), quietLogger())

	if pack == "" {
		t.Fatalf("treatment task got no pack; stats=%+v", stats)
	}
	if stats == nil || stats.Arm != 1 {
		t.Fatalf("stats arm = %+v, want arm 1", stats)
	}
	if stats.Degraded {
		t.Errorf("stats say degraded but a pack was returned: %+v", stats)
	}
	if !strings.Contains(pack, "internal/issue/status.go") {
		t.Errorf("pack missed the relevant file:\n%s", pack)
	}
	// The untrusted-data framing must be present — the pack carries repo text.
	if !strings.Contains(pack, "NOT instructions") {
		t.Errorf("pack missing prompt-injection framing:\n%s", pack)
	}
	if strings.Contains(pack, "sk_live_must_never_appear") {
		t.Fatalf("pack leaked .env contents:\n%s", pack)
	}
	if stats.PackTokens > repoindex.DefaultTokenBudget {
		t.Errorf("pack = %d tokens, over the %d budget", stats.PackTokens, repoindex.DefaultTokenBudget)
	}
}

// TestBuildRepoPackControlIsInert: a control task must be byte-identical to
// today's behavior, yet still be recorded so the A/B has a denominator.
func TestBuildRepoPackControlIsInert(t *testing.T) {
	d := &Daemon{}
	task := Task{
		ID:         findArmTaskID(t, 0),
		IssueID:    "MUL-2",
		ThreadName: "Update issue status transition",
	}
	pack, stats := d.buildRepoPack(context.Background(), task, writeTestRepo(t), quietLogger())

	if pack != "" {
		t.Errorf("control task received a pack:\n%s", pack)
	}
	if stats == nil {
		t.Fatal("control task was not recorded — the A/B loses its denominator")
	}
	if stats.Arm != 0 {
		t.Errorf("stats arm = %d, want 0", stats.Arm)
	}
}

// TestBuildRepoPackIneligibleIsUnrecorded: a task that could never benefit is
// outside the experiment entirely — no pack AND no row.
func TestBuildRepoPackIneligibleIsUnrecorded(t *testing.T) {
	d := &Daemon{}
	task := Task{ID: findArmTaskID(t, 1), QuickCreatePrompt: "make me an issue"}
	pack, stats := d.buildRepoPack(context.Background(), task, writeTestRepo(t), quietLogger())
	if pack != "" || stats != nil {
		t.Errorf("ineligible task: pack=%q stats=%+v, want empty/nil", pack, stats)
	}
}

// TestBuildRepoPackOptOut pins the operator kill switch.
func TestBuildRepoPackOptOut(t *testing.T) {
	t.Setenv("AGORA_REPO_INDEX_DISABLED", "1")
	d := &Daemon{}
	task := Task{ID: findArmTaskID(t, 1), IssueID: "MUL-3", ThreadName: "Update issue status"}
	pack, stats := d.buildRepoPack(context.Background(), task, writeTestRepo(t), quietLogger())
	if pack != "" || stats != nil {
		t.Errorf("opt-out ignored: pack=%q stats=%+v", pack, stats)
	}
}

// TestBuildRepoPackMissingWorkDirFailsSoft: an unreadable workdir must never
// fail a task — the pack is an optimization, not a dependency.
func TestBuildRepoPackMissingWorkDirFailsSoft(t *testing.T) {
	d := &Daemon{}
	task := Task{ID: findArmTaskID(t, 1), IssueID: "MUL-4", ThreadName: "Update issue status"}

	if pack, stats := d.buildRepoPack(context.Background(), task, "", quietLogger()); pack != "" || stats != nil {
		t.Errorf("empty workdir: pack=%q stats=%+v", pack, stats)
	}
	pack, stats := d.buildRepoPack(context.Background(), task, filepath.Join(t.TempDir(), "does-not-exist"), quietLogger())
	if pack != "" {
		t.Errorf("nonexistent workdir produced a pack:\n%s", pack)
	}
	if stats == nil || !stats.Degraded {
		t.Errorf("nonexistent workdir should record a degraded treatment row, got %+v", stats)
	}
}
