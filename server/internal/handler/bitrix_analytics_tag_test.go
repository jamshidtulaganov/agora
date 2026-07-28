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
