package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildOrchestrationPromptCarriesStepContract(t *testing.T) {
	out := BuildPrompt(Task{
		IssueID:                   "issue-1",
		OrchestrationStepID:       "step-1",
		OrchestrationStepTitle:    "Verify authentication",
		OrchestrationStage:        "qa",
		OrchestrationInstructions: "Run the browser suite and report failing cases.",
	}, "codex")
	for _, want := range []string{"Verify authentication", "Stage: qa", "Run the browser suite", "Stage contract", "verdict to pass or fail", "engine owns issue status", "do not change the issue status", "run_qa", "@mention another agent", "inspect the issue", "durable orchestration messages", "Do not guess", "outcome `waiting_input`", "one precise durable `question`", "```agora-handoff"} {
		if !strings.Contains(out, want) {
			t.Fatalf("orchestration prompt missing %q:\n%s", want, out)
		}
	}
}

func TestBuildOrchestrationPromptUsesHumanClarificationTarget(t *testing.T) {
	prompt := buildOrchestrationPrompt(Task{IssueID: "ISSUE-1", OrchestrationStepID: "step-1", OrchestrationStage: "dev"})
	if !strings.Contains(prompt, "`target` set to `human`") {
		t.Fatalf("prompt must route blocking questions to the implemented human consumer: %s", prompt)
	}
	if strings.Contains(prompt, "`human`, `controller`, or `agent`") {
		t.Fatal("prompt advertises unsupported agent/controller question delivery")
	}
}

func TestBuildOrchestrationPromptCarriesIntegrationContract(t *testing.T) {
	out := BuildPrompt(Task{
		IssueID:                "issue-1",
		OrchestrationStepID:    "step-integrate",
		OrchestrationStepKind:  "integration",
		OrchestrationStepTitle: "Integrate parallel branches",
		OrchestrationStage:     "review",
		OrchestrationBaseRefs:  []OrchestrationGitHead{{Repo: "api", HeadSHA: "base-api"}, {Repo: "web", HeadSHA: "base-web"}},
		Repos:                  []RepoData{{URL: "https://github.com/acme/api.git"}, {URL: "https://github.com/acme/web.git"}},
		OrchestrationDependencies: []OrchestrationGitDependency{
			{Key: "api", Branch: "agent/api", HeadSHA: "abc123", Heads: []OrchestrationGitHead{{Repo: "api", HeadSHA: "abc123"}, {Repo: "web", HeadSHA: "abc-web"}}, Handoff: []byte(`{"schema_version":1,"stage":"dev","outcome":"completed","summary":"API ready","contracts":["GET /items"]}`)},
			{Key: "web", Branch: "agent/web", HeadSHA: "def456"},
		},
	}, "codex")
	for _, want := range []string{"enforced integration gate", "base-api", "agora repo checkout \"https://github.com/acme/api.git\" --ref base-api", "repo=api", "abc123", "repo=web", "abc-web", "agent/web", "def456", "multi-repo dependencies", "git merge --no-ff", "independently verify", "Authoritative dependency handoffs", "API ready", "GET /items"} {
		if !strings.Contains(out, want) {
			t.Fatalf("integration prompt missing %q:\n%s", want, out)
		}
	}
}

func TestBuildOrchestrationPromptCarriesReadOnlyVerificationContract(t *testing.T) {
	out := BuildPrompt(Task{
		IssueID:                "issue-1",
		OrchestrationStepID:    "step-qa",
		OrchestrationStepTitle: "Verify integrated result",
		OrchestrationStage:     "qa",
		OrchestrationReadOnly:  true,
		PreprovisionedWorktree: true,
		OrchestrationBaseRefs:  []OrchestrationGitHead{{Repo: "api", HeadSHA: "abc"}},
	}, "codex")
	for _, want := range []string{"read-only verification step", "exact integrated commit", "repository is already present in the current working directory", "do not run `agora repo checkout`", "do not edit files", "move HEAD"} {
		if !strings.Contains(out, want) {
			t.Fatalf("read-only prompt missing %q:\n%s", want, out)
		}
	}
}

func TestBuildOrchestrationPromptChecksOutRemoteVerificationHead(t *testing.T) {
	out := BuildPrompt(Task{
		IssueID:                "issue-remote",
		OrchestrationStepID:    "step-review",
		OrchestrationStepTitle: "Review integrated result",
		OrchestrationStage:     "review",
		OrchestrationReadOnly:  true,
		Repos:                  []RepoData{{URL: "git@github.com:acme/api.git"}},
		OrchestrationBaseRefs:  []OrchestrationGitHead{{Repo: "api", HeadSHA: "deadbeef"}},
	}, "codex")
	for _, want := range []string{"same-step continuation may already contain the exact checkout", "reuse it only when its HEAD matches", "repo=api head=deadbeef", "agora repo checkout \"git@github.com:acme/api.git\" --ref deadbeef", "managed checkout may create its task branch", "Run non-mutating checks only"} {
		if !strings.Contains(out, want) {
			t.Fatalf("remote verification prompt missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "repository is already present in the current working directory") {
		t.Fatalf("remote verification prompt falsely claims a preprovisioned checkout:\n%s", out)
	}
}

func TestBuildOrchestrationContextEnforcesAggregateBudgetAndKeepsCriticalRecentEntries(t *testing.T) {
	largePayload := func(summary, tail string) json.RawMessage {
		return json.RawMessage(`{"summary":"` + summary + strings.Repeat("界", 10_000) + `","next_actions":["` + tail + `"]}`)
	}

	dependencies := make([]OrchestrationGitDependency, 0, 6)
	for i := 0; i < 5; i++ {
		dependencies = append(dependencies, OrchestrationGitDependency{
			Key:     "older-dependency-" + string(rune('a'+i)),
			Handoff: largePayload("older-handoff-", "older-tail"),
		})
	}
	dependencies = append(dependencies, OrchestrationGitDependency{
		Key:     "latest-dependency",
		Handoff: largePayload("LATEST-HANDOFF", "LATEST-HANDOFF-TAIL"),
	})

	messages := make([]OrchestrationMessageEnvelope, 0, 12)
	for i := 0; i < 10; i++ {
		messages = append(messages, OrchestrationMessageEnvelope{
			Kind:      "handoff",
			ActorType: "agent",
			Body:      largePayload("older-message-", "older-message-tail"),
		})
	}
	messages = append(messages,
		OrchestrationMessageEnvelope{
			Kind:      "question",
			ActorType: "agent",
			Body:      largePayload("LATEST-QUESTION", "LATEST-QUESTION-TAIL"),
		},
		OrchestrationMessageEnvelope{
			Kind:      "answer",
			ActorType: "member",
			Body:      largePayload("LATEST-ANSWER", "LATEST-ANSWER-TAIL"),
		},
	)

	task := Task{
		OrchestrationDependencies: dependencies,
		OrchestrationMessages:     messages,
	}
	context := buildOrchestrationContext(task)
	if len(context) > orchestrationContextByteBudget {
		t.Fatalf("orchestration context is %d bytes, budget is %d", len(context), orchestrationContextByteBudget)
	}
	if !utf8.ValidString(context) {
		t.Fatal("byte truncation produced invalid UTF-8")
	}
	for _, want := range []string{
		"latest-dependency",
		"LATEST-HANDOFF",
		"LATEST-HANDOFF-TAIL",
		"LATEST-QUESTION",
		"LATEST-QUESTION-TAIL",
		"LATEST-ANSWER",
		"LATEST-ANSWER-TAIL",
		orchestrationContextTruncationMarker,
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("bounded context dropped critical marker %q:\n%s", want, context)
		}
	}
	if strings.Contains(context, "older-dependency-a") {
		t.Fatal("oldest dependency should yield to protected recent context")
	}
	if question, answer := strings.Index(context, "LATEST-QUESTION"), strings.Index(context, "LATEST-ANSWER"); question < 0 || answer < 0 || question >= answer {
		t.Fatalf("selected messages were not restored to chronological order: question=%d answer=%d", question, answer)
	}
	if repeated := buildOrchestrationContext(task); repeated != context {
		t.Fatal("bounded orchestration context is not deterministic")
	}
}

func TestBuildOrchestrationContextPreservesSmallEntries(t *testing.T) {
	task := Task{
		OrchestrationDependencies: []OrchestrationGitDependency{
			{Key: "implementation", Handoff: json.RawMessage(`{"summary":"ready"}`)},
		},
		OrchestrationMessages: []OrchestrationMessageEnvelope{
			{Kind: "question", ActorType: "agent", Body: json.RawMessage(`{"prompt":"Which version?"}`)},
			{Kind: "answer", ActorType: "member", Body: json.RawMessage(`{"answer":"v2"}`)},
		},
	}

	context := buildOrchestrationContext(task)
	for _, want := range []string{
		`- implementation: {"summary":"ready"}`,
		`- question from agent: {"prompt":"Which version?"}`,
		`- answer from member: {"answer":"v2"}`,
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("small orchestration context missing %q:\n%s", want, context)
		}
	}
	if strings.Contains(context, orchestrationContextTruncationMarker) {
		t.Fatal("small orchestration context was unnecessarily truncated")
	}
}

// TestBuildQuickCreatePromptRules locks in the rules that govern how the
// quick-create agent is allowed to translate raw user input into the issue
// description body. Each substring corresponds to a concrete failure mode
// observed in production output:
//   - meta-instructions ("create an issue", "cc @X") leaking into the body
//   - the Context section being misused as an apology log when no external
//     references were actually fetched
//   - hard-line rules being silently dropped on prompt rewrites
func TestBuildQuickCreatePromptRules(t *testing.T) {
	out := buildQuickCreatePrompt(Task{QuickCreatePrompt: "fix the login button color"})

	mustContain := []string{
		// high-fidelity invariant
		"Faithfully restate what the user wants",
		"Preserve specific names, identifiers, file paths",
		// strip non-spec material: verbal routing wrappers + conversational fillers
		"verbal routing wrappers about creating the issue",
		"pure conversational fillers",
		// cc routing must survive: mention link stays in description so the
		// auto-subscribe path fires (agora issue create has no --subscriber flag)
		"CC exception",
		"auto-subscribes members",
		// context section is conditional and must not be an apology log
		"include ONLY when the input cited external resources",
		"never use it as an apology log",
		// output/reporting must be workspace-prefix agnostic. Workspaces can
		// use custom issue prefixes, so a successful issue creation should
		// not look failed merely because the identifier does not match one
		// fixed prefix.
		"agora issue create --output json",
		"JSON response",
		"identifier",
		"Do not scrape human output",
		"do not assume any workspace issue prefix",
		"Created <identifier-or-id>: <title>",
		// hard rules
		"never invent requirements",
		"never reduce multi-sentence input",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildQuickCreatePrompt output missing required rule: %q", s)
		}
	}
}

// TestBuildQuickCreatePromptAssigneeIncludesSquads locks in the MUL-2165
// fix: the assignee-resolution rules must tell the agent to consult the
// squad list alongside members and agents. Before this, a quick-create
// input like "assign to <SquadName>" silently fell through to
// "Unrecognized assignee" because squads were never queried.
func TestBuildQuickCreatePromptAssigneeIncludesSquads(t *testing.T) {
	out := buildQuickCreatePrompt(Task{QuickCreatePrompt: "fix the login button color"})
	mustContain := []string{
		"agora squad list",
		"Squads are first-class assignees",
		"Treat bare @-routing as an assignee directive",
		"让 @独立团 review 这个 PR",
		"pass the squad's `id` as `--assignee-id`",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildQuickCreatePrompt assignee block missing %q\n--- output ---\n%s", s, out)
		}
	}
}

// TestBuildQuickCreatePromptSquadDefaultsToSquad locks in the MUL-2203
// fix: when the picker was a squad, the task runs on the squad's leader
// agent, but the default assignee for issues created by this run must
// point at the SQUAD's UUID — not the leader agent's UUID. The previous
// "default to YOURSELF" instruction made squad-created issues land under
// the leader, hiding them from the squad's delegation flow.
func TestBuildQuickCreatePromptSquadDefaultsToSquad(t *testing.T) {
	const (
		squadID   = "aaaa1111-2222-3333-4444-555555555555"
		squadName = "独立团"
		leaderID  = "bbbb1111-2222-3333-4444-666666666666"
	)
	out := buildQuickCreatePrompt(Task{
		QuickCreatePrompt: "fix the login button color",
		Agent:             &AgentData{ID: leaderID, Name: "leader-agent"},
		SquadID:           squadID,
		SquadName:         squadName,
	})

	// The default-assignee instruction must point at the squad UUID.
	if !strings.Contains(out, "--assignee-id \""+squadID+"\"") {
		t.Errorf("buildQuickCreatePrompt with SquadID must default to the squad's UUID, got:\n%s", out)
	}
	// And it must NOT tell the agent to default to itself (the leader).
	if strings.Contains(out, "--assignee-id \""+leaderID+"\"") {
		t.Errorf("buildQuickCreatePrompt with SquadID must NOT default to the leader agent's UUID, got:\n%s", out)
	}
	// The squad name should appear in the instruction so the agent has
	// human-readable context for the routing decision.
	if !strings.Contains(out, squadName) {
		t.Errorf("buildQuickCreatePrompt with SquadID should mention the squad name %q, got:\n%s", squadName, out)
	}
	// And the prompt must explicitly call out the squad-vs-leader rule
	// so the agent does not silently regress to "default to YOURSELF".
	mustContain := []string{
		"picker SQUAD",
		"running on the squad's behalf",
		"do not assign it to your own agent UUID",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildQuickCreatePrompt with SquadID missing %q\n--- output ---\n%s", s, out)
		}
	}
}

// TestBuildQuickCreatePromptProjectPinning verifies that when the user
// pins a project in the quick-create modal, the prompt instructs the agent
// to pass `--project <uuid>` exactly. Without this, the agent would re-read
// the workspace default and silently drop the user's selection — the same
// "I have to retype 'in project X' every time" failure mode the modal
// addition was meant to fix.
func TestBuildQuickCreatePromptProjectPinning(t *testing.T) {
	const projectID = "11111111-2222-3333-4444-555555555555"
	out := buildQuickCreatePrompt(Task{
		QuickCreatePrompt: "fix the login button color",
		ProjectID:         projectID,
		ProjectTitle:      "Web App",
	})
	mustContain := []string{
		"--project \"" + projectID + "\"",
		"Web App",
		"modal selection is authoritative",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildQuickCreatePrompt with project missing %q\n--- output ---\n%s", s, out)
		}
	}

	// Without a project, the prompt must keep the legacy "omit" instruction
	// so the agent doesn't accidentally start passing --project on plain
	// quick-create runs.
	plain := buildQuickCreatePrompt(Task{QuickCreatePrompt: "fix the login button color"})
	if !strings.Contains(plain, "**project**: omit") {
		t.Errorf("buildQuickCreatePrompt without project must keep the omit instruction, got:\n%s", plain)
	}
	if strings.Contains(plain, "--project") {
		t.Errorf("buildQuickCreatePrompt without project must NOT mention --project, got:\n%s", plain)
	}
}

// TestBuildQuickCreatePromptParentPinning verifies that when the user
// opened quick-create from "Add sub issue" on an existing issue, the prompt
// instructs the agent to pass `--parent <uuid>` so the new issue is filed
// as a sub-issue. The frontend already seeds parent_issue_id silently
// through the manual→agent switch, so this is the last hop that has to
// hold up — without the prompt instruction the agent would create a
// standalone issue and the sub-issue relationship would be silently
// dropped.
func TestBuildQuickCreatePromptParentPinning(t *testing.T) {
	const (
		parentID         = "33333333-2222-1111-4444-555555555555"
		parentIdentifier = "MUL-2534"
	)
	out := buildQuickCreatePrompt(Task{
		QuickCreatePrompt:     "fix the login button color",
		ParentIssueID:         parentID,
		ParentIssueIdentifier: parentIdentifier,
	})
	mustContain := []string{
		"--parent \"" + parentID + "\"",
		parentIdentifier,
		"modal entry point is authoritative",
		"filed as a sub-issue",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildQuickCreatePrompt with parent missing %q\n--- output ---\n%s", s, out)
		}
	}

	// When only the UUID is available (identifier lookup failed on claim),
	// the agent must still get the --parent instruction so the sub-issue
	// intent isn't silently dropped.
	uuidOnly := buildQuickCreatePrompt(Task{
		QuickCreatePrompt: "fix the login button color",
		ParentIssueID:     parentID,
	})
	if !strings.Contains(uuidOnly, "--parent \""+parentID+"\"") {
		t.Errorf("buildQuickCreatePrompt with parent UUID only must still pin --parent, got:\n%s", uuidOnly)
	}

	// Without a parent, the prompt must NOT mention --parent at all — a
	// plain quick-create run should not start filing sub-issues.
	plain := buildQuickCreatePrompt(Task{QuickCreatePrompt: "fix the login button color"})
	if strings.Contains(plain, "--parent") {
		t.Errorf("buildQuickCreatePrompt without parent must NOT mention --parent, got:\n%s", plain)
	}
}

// TestBuildPromptSquadLeaderNoActionForMemberTrigger verifies that the
// squad leader no_action prohibition is injected in the per-turn prompt
// regardless of whether the triggering comment was posted by an agent or
// a member. This was the root cause of the "LGTM is a pure acknowledgment
// — no reply needed. Exiting silently." noise comment: the prohibition
// only fired for agent-triggered comments, so member-triggered ones
// (like "LGTM") bypassed it.
func TestBuildPromptSquadLeaderNoActionForMemberTrigger(t *testing.T) {
	task := Task{
		IssueID:               "issue-123",
		TriggerCommentID:      "comment-456",
		TriggerCommentContent: "LGTM",
		TriggerAuthorType:     "member",
		TriggerAuthorName:     "Bohan",
		Agent: &AgentData{
			Instructions: "Some instructions\n\n## Squad Operating Protocol\n\nYou are the LEADER...",
		},
	}
	out := BuildPrompt(task, "claude")
	if !strings.Contains(out, "Squad leader no_action rule") {
		t.Errorf("buildCommentPrompt must inject squad leader no_action rule for member-triggered comments, got:\n%s", out)
	}
	if !strings.Contains(out, "DO NOT post any comment") {
		t.Errorf("buildCommentPrompt must contain DO NOT post prohibition for member-triggered squad leader, got:\n%s", out)
	}
}

// TestBuildPromptSquadLeaderNoActionForAgentTrigger verifies the rule also
// fires for agent-triggered comments (the original path that already worked).
func TestBuildPromptSquadLeaderNoActionForAgentTrigger(t *testing.T) {
	task := Task{
		IssueID:               "issue-123",
		TriggerCommentID:      "comment-456",
		TriggerCommentContent: "Deploy complete.",
		TriggerAuthorType:     "agent",
		TriggerAuthorName:     "deploy-boy",
		Agent: &AgentData{
			Instructions: "Some instructions\n\n## Squad Operating Protocol\n\nYou are the LEADER...",
		},
	}
	out := BuildPrompt(task, "claude")
	if !strings.Contains(out, "Squad leader no_action rule") {
		t.Errorf("buildCommentPrompt must inject squad leader no_action rule for agent-triggered comments, got:\n%s", out)
	}
}

func TestBuildChatPromptAttachmentIDsCanBeBoundToCreatedIssues(t *testing.T) {
	task := Task{
		ChatSessionID: "sess-1",
		ChatMessage:   "please create an issue with this screenshot",
		ChatMessageAttachments: []ChatAttachmentMeta{
			{ID: "019ec09d-6222-722b-bdfa-427b105d80be", Filename: "shot.png", ContentType: "image/png"},
		},
	}
	out := BuildPrompt(task, "claude")
	for _, want := range []string{
		"Attachments on this message:",
		"id=019ec09d-6222-722b-bdfa-427b105d80be",
		"agora attachment download <id>",
		"--attachment-id <id>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chat prompt missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestBuildChatPromptSlashSkills(t *testing.T) {
	t.Run("injects selected skills block", func(t *testing.T) {
		task := Task{
			ChatSessionID: "sess-1",
			ChatMessage:   "please [/deploy](slash://skill/abc-123) this",
			Agent: &AgentData{
				Skills: []SkillData{{ID: "abc-123", Name: "deploy"}},
			},
		}
		out := buildChatPrompt(task)
		if !strings.Contains(out, "Explicitly selected skills:\n- deploy\n") {
			t.Fatalf("expected selected skills block, got:\n%s", out)
		}
		if !strings.Contains(out, "User message:\nplease [/deploy](slash://skill/abc-123) this") {
			t.Fatalf("expected raw user message preserved, got:\n%s", out)
		}
	})

	t.Run("ignores skills not belonging to agent", func(t *testing.T) {
		task := Task{
			ChatSessionID: "sess-1",
			ChatMessage:   "[/hacker-skill](slash://skill/evil-id)",
			Agent: &AgentData{
				Skills: []SkillData{{ID: "good-id", Name: "deploy"}},
			},
		}
		out := buildChatPrompt(task)
		if strings.Contains(out, "Explicitly selected skills") {
			t.Fatalf("should not inject block for unknown skill ID, got:\n%s", out)
		}
	})

	t.Run("validates by ID not label", func(t *testing.T) {
		task := Task{
			ChatSessionID: "sess-1",
			ChatMessage:   "[/deploy](slash://skill/wrong-id)",
			Agent: &AgentData{
				Skills: []SkillData{{ID: "real-id", Name: "deploy"}},
			},
		}
		out := buildChatPrompt(task)
		if strings.Contains(out, "Explicitly selected skills") {
			t.Fatalf("matching label with wrong ID must not pass, got:\n%s", out)
		}
	})

	t.Run("uses canonical name not label", func(t *testing.T) {
		task := Task{
			ChatSessionID: "sess-1",
			ChatMessage:   "[/spoofed-name](slash://skill/real-id)",
			Agent: &AgentData{
				Skills: []SkillData{{ID: "real-id", Name: "deploy"}},
			},
		}
		out := buildChatPrompt(task)
		if !strings.Contains(out, "- deploy\n") {
			t.Fatalf("expected canonical name 'deploy', got:\n%s", out)
		}
		if strings.Contains(out, "- spoofed-name\n") {
			t.Fatalf("selected skills block must not use spoofed label, got:\n%s", out)
		}
		if !strings.Contains(out, "User message:\n[/spoofed-name](slash://skill/real-id)") {
			t.Fatalf("expected raw user message with spoofed label preserved, got:\n%s", out)
		}
	})

	t.Run("deduplicates skills", func(t *testing.T) {
		task := Task{
			ChatSessionID: "sess-1",
			ChatMessage:   "[/deploy](slash://skill/a) and [/deploy](slash://skill/a) again",
			Agent: &AgentData{
				Skills: []SkillData{{ID: "a", Name: "deploy"}},
			},
		}
		out := buildChatPrompt(task)
		if strings.Count(out, "- deploy") != 1 {
			t.Fatalf("expected exactly 1 '- deploy', got:\n%s", out)
		}
	})

	t.Run("omits block when no valid skills", func(t *testing.T) {
		task := Task{
			ChatSessionID: "sess-1",
			ChatMessage:   "just a normal message",
			Agent:         &AgentData{Skills: []SkillData{{ID: "a", Name: "deploy"}}},
		}
		out := buildChatPrompt(task)
		if strings.Contains(out, "Explicitly selected skills") {
			t.Fatalf("should not inject block when no slash links, got:\n%s", out)
		}
	})

	t.Run("omits block when agent has no skills", func(t *testing.T) {
		task := Task{
			ChatSessionID: "sess-1",
			ChatMessage:   "[/deploy](slash://skill/abc-123)",
			Agent:         &AgentData{},
		}
		out := buildChatPrompt(task)
		if strings.Contains(out, "Explicitly selected skills") {
			t.Fatalf("should not inject block for agent with no skills, got:\n%s", out)
		}
	})
}

// TestBuildPromptDefaultMentionsRecent pins that the catch-all fallback
// prompt (no trigger comment, no chat, no autopilot, no quick-create) also
// teaches the agent about --recent as the long-issue-friendly alternative
// to the flat dump, even though it cannot anchor a --thread without a
// trigger comment id.
func TestBuildPromptDefaultMentionsRecent(t *testing.T) {
	out := BuildPrompt(Task{IssueID: "issue-default-1"}, "claude")
	for _, s := range []string{
		"--recent 20 --output json",
		"Next thread cursor:",
		"--since",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("default BuildPrompt missing %q\n--- output ---\n%s", s, out)
		}
	}
	// And the default path must NOT inject a --thread example, because there
	// is no trigger comment id to anchor on.
	if strings.Contains(out, "--thread") {
		t.Errorf("default BuildPrompt should NOT mention --thread (no trigger comment to anchor on)\n--- output ---\n%s", out)
	}
	// The legacy "If you need comment history" soft phrasing conflicts with
	// the assignment-trigger runtime workflow, which treats reading comments
	// as mandatory. Guard against it sneaking back in.
	if strings.Contains(out, "If you need comment history") {
		t.Errorf("default BuildPrompt still carries the legacy 'If you need' soft phrasing that conflicts with the mandatory workflow\n--- output ---\n%s", out)
	}
}

// TestBuildPromptNonSquadLeaderNoRule verifies that non-squad-leader agents
// do NOT get the squad leader no_action rule injected.
func TestBuildPromptNonSquadLeaderNoRule(t *testing.T) {
	task := Task{
		IssueID:               "issue-123",
		TriggerCommentID:      "comment-456",
		TriggerCommentContent: "LGTM",
		TriggerAuthorType:     "member",
		TriggerAuthorName:     "Bohan",
		Agent: &AgentData{
			Instructions: "Some instructions without the squad marker",
		},
	}
	out := BuildPrompt(task, "claude")
	if strings.Contains(out, "Squad leader no_action rule") {
		t.Errorf("buildCommentPrompt must NOT inject squad leader no_action rule for non-squad-leader agents, got:\n%s", out)
	}
}

// TestBuildPromptNewCommentsHint pins that a comment-triggered task whose agent
// ran before on this issue (NewCommentsSince set, NewCommentCount > 0) gets the
// since-delta hint with the ISSUE-WIDE new-comment count, but is steered to read
// the triggering (parent) thread first rather than blindly pulling every new
// comment.
func TestBuildPromptNewCommentsHint(t *testing.T) {
	const (
		issueID = "issue-new-1"
		since   = "2026-05-28T11:00:00Z"
	)
	task := Task{
		IssueID:               issueID,
		TriggerCommentID:      "trigger-1",
		TriggerThreadID:       "thread-root-1",
		TriggerCommentContent: "please look",
		TriggerAuthorType:     "member",
		NewCommentCount:       3,
		NewCommentsSince:      since,
	}
	out := BuildPrompt(task, "claude")

	// Issue-wide count (reverted from the thread-scoped wording).
	if !strings.Contains(out, "3 new comment(s) on this issue since your last run") {
		t.Errorf("hint must report the issue-wide new-comment count, got:\n%s", out)
	}
	// Don't-blindly-read-all guidance.
	if !strings.Contains(out, "blindly") {
		t.Errorf("hint must discourage blindly reading every new comment, got:\n%s", out)
	}
	// Parent thread first: the --thread <trigger> read is the prioritized action.
	if !strings.Contains(out, "agora issue comment list "+issueID+" --thread thread-root-1 --since "+since+" --output json") {
		t.Errorf("hint must point at the triggering (parent) thread --since read first, got:\n%s", out)
	}
	if !strings.Contains(out, "--tail 30") {
		t.Errorf("hint must offer the full-thread (--tail 30) option, got:\n%s", out)
	}
	// Issue-wide catch-up is demoted to an only-if-needed fallback.
	if !strings.Contains(out, "agora issue comment list "+issueID+" --since "+since+" --output json") {
		t.Errorf("hint must keep the issue-wide --since catch-up as a fallback, got:\n%s", out)
	}
	// The old cursor-heavy paragraph must be gone.
	if strings.Contains(out, "Next reply cursor") || strings.Contains(out, "--before-id") {
		t.Errorf("the old cursor-pagination paragraph must not render, got:\n%s", out)
	}
}

// TestBuildPromptColdStartThreadRead pins the cold-start case: no prior run means
// no since anchor (NewCommentsSince empty), so we suppress the delta hint and
// instead point the agent at the triggering CONVERSATION (--thread <trigger>
// --tail 30) rather than dumping the flat timeline.
func TestBuildPromptColdStartThreadRead(t *testing.T) {
	const issueID = "issue-cold-1"
	task := Task{
		IssueID:               issueID,
		TriggerCommentID:      "trigger-1",
		TriggerThreadID:       "thread-root-1",
		TriggerCommentContent: "hi",
		TriggerAuthorType:     "member",
		NewCommentCount:       0,
		NewCommentsSince:      "",
	}
	out := BuildPrompt(task, "claude")
	if strings.Contains(out, "new comment(s) since your last run") {
		t.Errorf("no since-delta hint should render on cold start, got:\n%s", out)
	}
	if !strings.Contains(out, "agora issue comment list "+issueID+" --thread thread-root-1 --tail 30 --output json") {
		t.Errorf("cold start must point at the triggering thread read, got:\n%s", out)
	}
}

// TestBuildPromptResumedNoDeltaDoesNotForceThreadRead pins the warm/no-delta
// path: when a prior provider session is actually being resumed, the triggering
// comment is already embedded in the per-turn prompt, so the agent should not
// be told to re-read the triggering thread's latest 30 replies by default.
func TestBuildPromptResumedNoDeltaDoesNotForceThreadRead(t *testing.T) {
	const issueID = "issue-resumed-1"
	task := Task{
		IssueID:               issueID,
		TriggerCommentID:      "trigger-1",
		TriggerThreadID:       "thread-root-1",
		TriggerCommentContent: "hi again",
		TriggerAuthorType:     "member",
		PriorSessionID:        "session-123",
		NewCommentCount:       0,
		NewCommentsSince:      "",
	}
	out := BuildPrompt(task, "claude")

	for _, want := range []string{
		"triggering comment is already included above",
		"No other new comments on this issue since your last run",
		"active thread anchor `thread-root-1` and triggering comment ID `trigger-1`",
		"If your reply depends on thread context",
		"do not rely only on resumed session memory",
		"agora issue comment list " + issueID + " --thread thread-root-1 --tail 30 --output json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("resumed/no-delta prompt missing %q\n--- output ---\n%s", want, out)
		}
	}
	// The stale thread-scoped wording (since-delta used to be thread-scoped)
	// must not reappear.
	if strings.Contains(out, "scoped to the triggering thread") {
		t.Errorf("resumed/no-delta prompt must not claim the delta is thread-scoped, got:\n%s", out)
	}
	if strings.Contains(out, "Read the triggering conversation first") {
		t.Errorf("resumed/no-delta prompt must not use the cold-start forced-read wording, got:\n%s", out)
	}
}
