package assistant

import (
	"context"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

func TestReviewModeToolPolicy(t *testing.T) {
	for _, spec := range ToolSpecs() {
		want := (!IsMutating(spec.Name) && spec.Name != ToolDryRunImport) || spec.Name == ToolCreateArtifact || spec.Name == ToolUpdateArtifact
		if got := ReviewToolAllowed(spec.Name); got != want {
			t.Errorf("%s allowed = %v, want %v", spec.Name, got, want)
		}
	}
	for _, name := range []string{"unknown_read_tool", "create_future_tool", ToolProposePlan} {
		if ReviewToolAllowed(name) {
			t.Errorf("review admitted %s", name)
		}
	}
	ctx := WithRunMode(context.Background(), RunModeReview)
	s := &Service{}
	result, err := s.execute(ctx, "user", "session", llm.ToolCall{Name: ToolCreateIssue})
	if err == nil || string(result) != `{"error":"review mode is read-only; workspace changes and confirmation requests are not allowed"}` {
		t.Fatalf("write bypass: %s, %v", result, err)
	}
}

func TestResolveRunMode(t *testing.T) {
	for _, tc := range []struct{ mode RunMode; content string; want RunMode }{
		{"", "create a task", RunModeAssist},
		{"", "/review inspect this feature", RunModeReview},
		{RunModeAssist, " /review inspect this feature", RunModeReview},
		{RunModeReview, "create a task", RunModeReview},
	} {
		got, err := ResolveRunMode(tc.mode, tc.content)
		if err != nil || got != tc.want { t.Fatalf("%+v: %q, %v", tc, got, err) }
	}
	if _, err := ResolveRunMode("invalid", "hello"); err == nil { t.Fatal("invalid mode accepted") }
}
