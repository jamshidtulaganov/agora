package handler

import (
	"context"
	"testing"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// A non-owner member sees an issue's subtasks on the board and in the parent's
// subtask list — ListChildIssues is deliberately unfiltered — but opening one
// went through IssueBelongsToUser, which only asked whether that row itself was
// theirs. A subtask someone else created under a parent they own answered 404,
// so the UI showed a card that could not be opened.
//
// Ownership now walks the ancestor chain. These tests pin both directions:
// inheriting from a parent must work at depth, and it must not become a way to
// read an unrelated issue.
func subtaskVisibilityFixture(t *testing.T) (ws, member, parentID string) {
	t.Helper()
	owner := newAssistantTestUser(t, "subtask-owner@agora.dev")
	member = newAssistantTestUser(t, "subtask-member@agora.dev")
	ws = newAssistantTestWorkspace(t, "subtask-vis-ws", "SUB")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")
	// The parent is the member's: they are its assignee.
	parentID = newAssistantTestIssue(t, ws, "parent the member owns", owner, member)
	return ws, member, parentID
}

func setTestIssueParent(t *testing.T, childID, parentID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET parent_issue_id = $2 WHERE id = $1`, childID, parentID); err != nil {
		t.Fatalf("set parent: %v", err)
	}
}

func issueVisible(t *testing.T, ws, issueID, userID string) bool {
	t.Helper()
	owned, err := testHandler.Queries.IssueBelongsToUser(context.Background(), db.IssueBelongsToUserParams{
		IssueID:     parseUUID(issueID),
		WorkspaceID: parseUUID(ws),
		UserID:      parseUUID(userID),
	})
	if err != nil {
		t.Fatalf("IssueBelongsToUser: %v", err)
	}
	return owned
}

func TestSubtaskOfAnOwnedIssueIsVisible(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ws, member, parentID := subtaskVisibilityFixture(t)
	other := newAssistantTestUser(t, "subtask-other@agora.dev")
	addAssistantTestMember(t, ws, other, "member")

	// Created by somebody else, assigned to nobody — the member's only claim
	// on it is the parent.
	child := newAssistantTestIssue(t, ws, "subtask by someone else", other, "")
	setTestIssueParent(t, child, parentID)

	if !issueVisible(t, ws, parentID, member) {
		t.Fatal("the member cannot see the parent they are assigned — fixture is wrong")
	}
	if !issueVisible(t, ws, child, member) {
		t.Error("a subtask of an issue the member owns is not visible — the board shows it and opening it 404s")
	}
}

func TestSubtaskVisibilityWalksTheWholeChain(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ws, member, parentID := subtaskVisibilityFixture(t)
	other := newAssistantTestUser(t, "subtask-deep@agora.dev")
	addAssistantTestMember(t, ws, other, "member")

	// Subtasks nest — the product rejects a cycle, not depth.
	prev := parentID
	var deepest string
	for i := 0; i < 3; i++ {
		id := newAssistantTestIssue(t, ws, "nested", other, "")
		setTestIssueParent(t, id, prev)
		prev, deepest = id, id
	}
	if !issueVisible(t, ws, deepest, member) {
		t.Error("a subtask three levels under an owned issue is not visible")
	}
}

func TestInheritedVisibilityDoesNotLeakUnrelatedIssues(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ws, member, _ := subtaskVisibilityFixture(t)
	other := newAssistantTestUser(t, "subtask-stranger@agora.dev")
	addAssistantTestMember(t, ws, other, "member")

	// No relationship to the member at all.
	unrelated := newAssistantTestIssue(t, ws, "nothing to do with them", other, other)
	if issueVisible(t, ws, unrelated, member) {
		t.Error("an unrelated issue became visible — inheritance must only follow parents")
	}

	// A subtask of that unrelated issue stays invisible too: inheritance goes
	// UP the chain, never down or sideways.
	child := newAssistantTestIssue(t, ws, "child of the unrelated one", other, other)
	setTestIssueParent(t, child, unrelated)
	if issueVisible(t, ws, child, member) {
		t.Error("a subtask of an unrelated issue became visible")
	}
}

// A parent pointing at itself is rejected on write, but a gate must not hang
// if data ever drifts — the walk is depth-capped.
func TestVisibilityWalkTerminatesOnACycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ws, member, _ := subtaskVisibilityFixture(t)
	other := newAssistantTestUser(t, "subtask-cycle@agora.dev")
	addAssistantTestMember(t, ws, other, "member")

	a := newAssistantTestIssue(t, ws, "cycle a", other, "")
	b := newAssistantTestIssue(t, ws, "cycle b", other, "")
	setTestIssueParent(t, a, b)
	setTestIssueParent(t, b, a)

	done := make(chan bool, 1)
	go func() { done <- issueVisible(t, ws, a, member) }()
	select {
	case visible := <-done:
		if visible {
			t.Error("a cycle unrelated to the member must not be visible")
		}
	case <-context.Background().Done():
	}
}
