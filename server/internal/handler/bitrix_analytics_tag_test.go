package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
)

func TestMatchesTagFoldsCase(t *testing.T) {
	// Bitrix tags are free text typed by humans, so "BUG", "bug" and "Bug" are
	// one tag in everything except storage. An exact-match filter would answer
	// "0 BUG tasks" for a portal that types them lowercase.
	task := &bitrix.Task{Tags: []string{"Bug", " urgent "}}
	for _, want := range []string{"bug", "BUG", "Bug"} {
		if !matchesTag(task, want) {
			t.Errorf("%q did not match tag Bug", want)
		}
	}
	// Surrounding whitespace in the stored tag must not defeat a match.
	if !matchesTag(task, "urgent") {
		t.Error("a padded tag must still match")
	}
	if matchesTag(task, "feature") {
		t.Error("an absent tag must not match")
	}
	if matchesTag(&bitrix.Task{}, "bug") {
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
		Tag: "bug", Assignee: "10", Creator: "525", Group: "17",
		Status: "open", Priority: "2", Stage: "code review", Title: "hisobot",
	}
	if !all.matches(task) {
		t.Fatal("a task satisfying every filter was rejected")
	}
	// Flipping any single one must reject it — otherwise a filter silently
	// does nothing and its number is reported as if it applied.
	for name, f := range map[string]bitrixAnalyticsFilters{
		"tag":      {Tag: "feature"},
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
	} {
		if !bitrixFilterParams[key] {
			t.Errorf("%s would be rejected as unknown", key)
		}
	}
	if bitrixFilterParams["priorty"] {
		t.Error("a misspelling must not be accepted")
	}
}
