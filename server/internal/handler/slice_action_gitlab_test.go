package handler

import (
	"strings"
	"testing"
)

func TestBranchInstructionFor(t *testing.T) {
	t.Setenv("AGORA_GITLAB_MR_TARGET", "")

	gitlab := branchInstructionFor(true, "btx-41261", "master")
	for _, want := range []string{"GitLab", "merge_request.create", "btx-41261", "`master`", "merge_request.target=master"} {
		if !strings.Contains(gitlab, want) {
			t.Errorf("gitlab instruction missing %q: %s", want, gitlab)
		}
	}
	if strings.Contains(gitlab, "billing") || strings.Contains(gitlab, "gh ") {
		t.Errorf("gitlab instruction must not mention billing/gh: %s", gitlab)
	}

	gitlabNoBranch := branchInstructionFor(true, "", "")
	if !strings.Contains(gitlabNoBranch, "a short descriptive") || !strings.Contains(gitlabNoBranch, "merge_request.create") {
		t.Errorf("gitlab no-branch instruction wrong: %s", gitlabNoBranch)
	}

	github := branchInstructionFor(false, "btx-99", "ignored")
	for _, want := range []string{"billing", "btx-99", "pull request"} {
		if !strings.Contains(github, want) {
			t.Errorf("github instruction missing %q: %s", want, github)
		}
	}
	if strings.Contains(github, "merge_request") {
		t.Errorf("github instruction must not mention merge_request: %s", github)
	}

	if got := branchInstructionFor(false, "", ""); got != "" {
		t.Errorf("github no-branch should be empty, got: %s", got)
	}

	t.Setenv("AGORA_GITLAB_MR_TARGET", "staging")
	gitlabOverride := branchInstructionFor(true, "btx-41261", "master")
	if !strings.Contains(gitlabOverride, "merge_request.target=staging") {
		t.Errorf("gitlab environment override not honored: %s", gitlabOverride)
	}
}
