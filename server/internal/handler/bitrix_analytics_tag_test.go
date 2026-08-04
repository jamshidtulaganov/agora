package handler

import (
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
)

func TestMatchesTagFoldsCase(t *testing.T) {
	// Bitrix tags are free text typed by humans, so "BUG", "bug" and "Bug" are
	// one tag in everything except storage. An exact-match filter would answer
	// "0 BUG tasks" for a portal that types them lowercase.
	task := &bitrix.Task{Tags: []string{"Bug", " urgent "}}
	for _, want := range []string{"bug", "BUG", "Bug"} {
		if !matchesTag(task, expandTagQuery(want)) {
			t.Errorf("%q did not match tag Bug", want)
		}
	}
	// Surrounding whitespace in the stored tag must not defeat a match.
	if !matchesTag(task, expandTagQuery("urgent")) {
		t.Error("a padded tag must still match")
	}
	if matchesTag(task, expandTagQuery("рефакторинг")) {
		t.Error("an absent tag must not match")
	}
	if matchesTag(&bitrix.Task{}, expandTagQuery("bug")) {
		t.Error("a task with no tags must not match")
	}
}

func TestFiltersCompose(t *testing.T) {
	// Filters must AND together: "BUG tasks opened by 525 that are still open"
	// is one question, and answering it as three separate rollups is what the
	// composition exists to avoid.
	task := &bitrix.Task{
		Tags: []string{"BUG"}, ResponsibleID: "10", CreatedByID: "525",
		GroupID: "17", Status: "3", Priority: "2", StageID: "Code Review",
		Title: "Hisobot sahifasi ochilmayapti",
	}
	all := bitrixAnalyticsFilters{
		Tag: "bug", tagSpellings: expandTagQuery("bug"), Assignee: "10", Creator: "525", Group: "17",
		Status: "open", Priority: "2", Stage: "code review", Title: "hisobot",
	}
	if !all.matches(task) {
		t.Fatal("a task satisfying every filter was rejected")
	}
	// Flipping any single one must reject it — otherwise a filter silently
	// does nothing and its number is reported as if it applied.
	for name, f := range map[string]bitrixAnalyticsFilters{
		"tag":      {Tag: "feature", tagSpellings: expandTagQuery("feature")},
		"assignee": {Assignee: "11"},
		"creator":  {Creator: "526"},
		"group":    {Group: "18"},
		"status":   {Status: "completed"},
		"priority": {Priority: "1"},
		"stage":    {Stage: "Testing"},
		"title":    {Title: "login"},
	} {
		if f.matches(task) {
			t.Errorf("%s filter did not exclude a non-matching task", name)
		}
	}
}

func TestStatusFilterAcceptsBucketOrRawCode(t *testing.T) {
	// by_status reports bucket names, so requiring the numeric code here would
	// force the caller to translate what the response just told them.
	done := &bitrix.Task{Status: "5"}
	if !(bitrixAnalyticsFilters{Status: "completed"}).matches(done) {
		t.Error("bucket name did not match")
	}
	if !(bitrixAnalyticsFilters{Status: "5"}).matches(done) {
		t.Error("raw status code did not match")
	}
	if (bitrixAnalyticsFilters{Status: "open"}).matches(done) {
		t.Error("a completed task matched the open bucket")
	}
}

func TestClosedFilterUsesTheTimestamp(t *testing.T) {
	closed := &bitrix.Task{ClosedAt: "2026-03-04T09:00:00+05:00"}
	open := &bitrix.Task{}
	if !(bitrixAnalyticsFilters{Closed: "true"}).matches(closed) {
		t.Error("closed=true rejected a closed task")
	}
	if (bitrixAnalyticsFilters{Closed: "true"}).matches(open) {
		t.Error("closed=true accepted a task with no close date")
	}
	if !(bitrixAnalyticsFilters{Closed: "false"}).matches(open) {
		t.Error("closed=false rejected an open task")
	}
}

func TestEmptyFiltersConstrainNothing(t *testing.T) {
	var none bitrixAnalyticsFilters
	if none.active() {
		t.Error("an empty filter set must not report itself active")
	}
	if !none.matches(&bitrix.Task{}) {
		t.Error("an empty filter set must accept every task")
	}
}

func TestEveryFilterParamIsAccepted(t *testing.T) {
	// The guard: an unknown parameter is a 400, so a typo cannot return the
	// portal-wide rollup dressed as a filtered one. That only holds if every
	// real parameter is on the list.
	for _, key := range []string{
		"since", "until", "tag", "assignee", "creator", "group",
		"status", "stage", "priority", "title", "closed",
		"sprint", "flow", "parent", "involves", "overdue",
		"deadline_from", "deadline_to", "closed_from", "closed_to",
	} {
		if !bitrixFilterParams[key] {
			t.Errorf("%s would be rejected as unknown", key)
		}
	}
	if bitrixFilterParams["priorty"] {
		t.Error("a misspelling must not be accepted")
	}
}

func TestOverdueIgnoresClosedTasks(t *testing.T) {
	// A closed task that missed its deadline is history, not something to act
	// on. Counting it would make the overdue number grow forever and stop
	// meaning anything.
	past := time.Now().AddDate(0, 0, -5).Format("2006-01-02T15:04:05-07:00")
	if !taskIsOverdue(&bitrix.Task{Deadline: past}) {
		t.Error("an open task past its deadline must count as overdue")
	}
	if taskIsOverdue(&bitrix.Task{Deadline: past, ClosedAt: past}) {
		t.Error("a closed task must not count as overdue")
	}
	future := time.Now().AddDate(0, 0, 5).Format("2006-01-02T15:04:05-07:00")
	if taskIsOverdue(&bitrix.Task{Deadline: future}) {
		t.Error("a task due in the future is not overdue")
	}
	// No deadline is not "overdue" — it is undated, which is a different
	// problem and is reported separately.
	if taskIsOverdue(&bitrix.Task{}) {
		t.Error("a task with no deadline must not count as overdue")
	}
}

func TestInvolvesMatchesEveryRole(t *testing.T) {
	// "How loaded is this person" is a different question from "what were they
	// assigned": an auditor on twenty tasks carries real load that
	// by_assignee shows as zero.
	task := &bitrix.Task{ResponsibleID: "10", Accomplices: []string{"20"}, Auditors: []string{"30"}}
	for _, id := range []string{"10", "20", "30"} {
		if !taskInvolves(task, id) {
			t.Errorf("user %s is on the task but was not matched", id)
		}
	}
	if taskInvolves(task, "40") {
		t.Error("an uninvolved user matched")
	}
}

func TestDeadlineWindowExcludesUndatedTasks(t *testing.T) {
	// A task with no deadline cannot satisfy a deadline window. Treating it as
	// a match would drop every undated task into a "due this week" answer.
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC)
	f := bitrixAnalyticsFilters{DeadlineFrom: "2026-07-01", deadlineFrom: from, DeadlineTo: "2026-07-31", deadlineTo: to}
	if f.matches(&bitrix.Task{}) {
		t.Error("an undated task matched a deadline window")
	}
	if !f.matches(&bitrix.Task{Deadline: "2026-07-15T10:00:00+05:00"}) {
		t.Error("a task inside the window was rejected")
	}
	if f.matches(&bitrix.Task{Deadline: "2026-08-02T10:00:00+05:00"}) {
		t.Error("a task after the window matched")
	}
}

func TestStageFilterAcceptsAName(t *testing.T) {
	// The stage is the field this team works in — To Do, Code Review, Need
	// merge — and nobody knows that Code Review is stage 1247. A name can map
	// to several ids because stages are defined per group.
	names := map[string]string{"1247": "Code Review", "1390": "Code review", "1250": "Testing"}
	ids := stageIDsNamed(names, "code review")
	if len(ids) != 2 || !ids["1247"] || !ids["1390"] {
		t.Fatalf("name resolved to %v, want both Code Review stages", ids)
	}
	f := bitrixAnalyticsFilters{Stage: "Code Review", stageIDs: ids}
	if !f.matches(&bitrix.Task{StageID: "1390"}) {
		t.Error("a task in a matching stage was rejected")
	}
	if f.matches(&bitrix.Task{StageID: "1250"}) {
		t.Error("a task in Testing matched Code Review")
	}
	// An id still works when no name resolves, so callers reading by_stage keys
	// are not forced to translate.
	byID := bitrixAnalyticsFilters{Stage: "1250"}
	if !byID.matches(&bitrix.Task{StageID: "1250"}) {
		t.Error("a raw stage id stopped working")
	}
}

func TestTagQueryExpandsAcrossLanguages(t *testing.T) {
	// The portal carries баг, BugReport, bug and #BugReport for one idea.
	// Exact match answers "how many bugs" with whichever spelling the asker
	// guessed — a wrong number that looks right.
	got := expandTagQuery("bug")
	for _, want := range []string{"bug", "баг", "bugreport"} {
		if !got[want] {
			t.Errorf("query 'bug' did not expand to %q", want)
		}
	}
	// The reverse direction matters just as much: people ask using the
	// spelling they see in Bitrix.
	fromRussian := expandTagQuery("баг")
	if !fromRussian["bug"] || !fromRussian["bugreport"] {
		t.Errorf("query 'баг' expanded to %v, missing the group", fromRussian)
	}
	// The '#' people type out of habit must not create a separate tag.
	if !expandTagQuery("#BugReport")["баг"] {
		t.Error("a leading # defeated the grouping")
	}
}

func TestTagQueryDoesNotWidenUnknownTags(t *testing.T) {
	// A label in no group matches only itself. Widening an unknown tag would
	// quietly inflate a count nobody can explain.
	got := expandTagQuery("Somafix2")
	if len(got) != 1 || !got["somafix2"] {
		t.Fatalf("unknown tag expanded to %v", got)
	}
}

func TestMatchesTagUsesTheExpandedSet(t *testing.T) {
	task := &bitrix.Task{Tags: []string{"BugReport", "баг"}}
	if !matchesTag(task, expandTagQuery("bug")) {
		t.Error("a russian-tagged task was missed by an english query")
	}
	if matchesTag(task, expandTagQuery("feature")) {
		t.Error("an unrelated group matched")
	}
	if matchesTag(&bitrix.Task{}, expandTagQuery("bug")) {
		t.Error("an untagged task matched")
	}
}

func TestNormalizeTagIsConservative(t *testing.T) {
	// Small on purpose: an aggressive normaliser would merge tags that are
	// genuinely different.
	if normalizeTag("  #BugReport ") != "bugreport" {
		t.Errorf("got %q", normalizeTag("  #BugReport "))
	}
	if normalizeTag("Настройка сервера") != "настройка сервера" {
		t.Error("internal spacing must survive normalisation")
	}
}
