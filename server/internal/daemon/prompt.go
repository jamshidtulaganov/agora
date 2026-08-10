package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jamshidtulaganov/agora/server/internal/daemon/execenv"
)

const (
	// Orchestration handoffs can contain many artifacts, checks, and findings,
	// while a resumed step can also carry several clarification rounds. Bound
	// their aggregate prompt contribution so durable coordination context cannot
	// crowd out the issue and stage contract.
	orchestrationContextByteBudget       = 48 * 1024
	orchestrationContextEntryByteBudget  = 12 * 1024
	orchestrationContextTruncationMarker = " ... [entry truncated by orchestration context budget] ... "
)

const (
	orchestrationDependencyContextHeader = "Authoritative dependency handoffs:\n"
	orchestrationDependencyContextFooter = "Treat these handoffs as the stage-to-stage source of truth. Read issue comments only for additional human discussion.\n\n"
	orchestrationMessageContextHeader    = "Messages for this work unit:\n"
	orchestrationMessageContextFooter    = "An `answer` resolves the preceding blocking question and is authoritative input for this continuation. Do not ask the same question again unless the answer is genuinely ambiguous.\n\n"
	orchestrationBoundedContextFooter    = "This coordination context is byte-bounded and prioritizes the latest dependency handoff and latest question/answer. Inspect the issue when an older detail is absent.\n\n"
)

// BuildPrompt constructs the task prompt for an agent CLI.
// Keep this minimal — detailed instructions live in CLAUDE.md / AGENTS.md
// injected by execenv.InjectRuntimeConfig. The provider string is threaded
// through to comment-triggered tasks' per-turn reply template; that template
// is provider-agnostic now (Linux/macOS → quoted-HEREDOC stdin, Windows →
// file) because the shell-layer corruption it guards against is not specific
// to any one provider (MUL-2904).
func BuildPrompt(task Task, provider string) string {
	if task.ChatSessionID != "" {
		return buildChatPrompt(task)
	}
	if task.TriggerCommentID != "" {
		return buildCommentPrompt(task, provider)
	}
	if task.AutopilotRunID != "" {
		return buildAutopilotPrompt(task)
	}
	if task.QuickCreatePrompt != "" {
		return buildQuickCreatePrompt(task)
	}
	if task.OrchestrationStepID != "" {
		return buildOrchestrationPrompt(task)
	}
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Agora workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	fmt.Fprintf(&b, "Start by running `agora issue get %s --output json` to understand your task, then complete it.\n", task.IssueID)
	fmt.Fprintf(&b, "For comment history, follow the rule in your runtime workflow file (assignment-triggered tasks treat the read as mandatory). `agora issue comment list %s --output json` returns all comments for the issue (server caps at 2000). On long-running issues use `--recent 20 --output json` to read the 20 most recently active threads, then page older threads via the stderr `Next thread cursor: ...` line and the matching `--before` / `--before-id` until you have enough history. `--since <RFC3339>` is still available for incremental polling and may combine with `--recent`.\n", task.IssueID)
	return b.String()
}

func buildOrchestrationPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are executing one step in a persisted multi-agent orchestration. Complete only this step and do not start a later stage yourself. The orchestration engine owns issue status, assignment, QA/review dispatch, and release: do not change the issue status or assignee, invoke run_qa/run_review/release actions, create side tasks, @mention another agent to start work, or spawn runtime-native subagents. All parallel work must be represented by persisted orchestration steps.\n\n")
	fmt.Fprintf(&b, "Issue ID: %s\n", task.IssueID)
	fmt.Fprintf(&b, "Orchestration step: %s\n", task.OrchestrationStepTitle)
	fmt.Fprintf(&b, "Stage: %s\n\n", task.OrchestrationStage)
	fmt.Fprintf(&b, "Stage contract:\n%s\n\n", orchestrationStageContract(task))
	if strings.TrimSpace(task.OrchestrationInstructions) != "" {
		fmt.Fprintf(&b, "Step instructions:\n%s\n\n", task.OrchestrationInstructions)
	}
	b.WriteString(buildOrchestrationContext(task))
	if task.OrchestrationReadOnly {
		if task.PreprovisionedWorktree {
			b.WriteString("This is a read-only verification step opened at the exact integrated commit. The repository is already present in the current working directory: do not run `agora repo checkout` or any command that fetches and checks out another ref. Inspect and run non-mutating checks only; do not edit files, create commits or branches, switch branches, reset, pull, or move HEAD. The daemon reports the repository state and the server rejects verification that no longer matches the integration handoff.\n\n")
		} else {
			b.WriteString("This is a read-only verification step for remote repositories. A same-step continuation may already contain the exact checkout: reuse it only when its HEAD matches the required SHA; otherwise fetch every exact integrated revision below with `agora repo checkout <url> --ref <exact-sha>` before inspecting it. That mandated managed checkout may create its task branch; after it completes, do not create or switch any additional branch, edit files, commit, reset, pull, or otherwise move HEAD. Run non-mutating checks only. The daemon reports repository state and the server rejects verification that does not match every integration head.\n")
			writeOrchestrationCheckoutRefs(&b, task, task.OrchestrationBaseRefs)
			b.WriteString("\n")
		}
	}
	if task.OrchestrationStepKind == "integration" {
		b.WriteString("This is an enforced integration gate. Merge every dependency commit below into this task's isolated branch, resolve conflicts, run the relevant verification, and leave the repository clean with all changes committed. Do not push, release, or claim success while a dependency is missing. The daemon will independently verify that every listed commit is an ancestor of your final HEAD; a prose summary cannot bypass this check.\n\n")
		if !task.PreprovisionedWorktree && len(task.OrchestrationBaseRefs) > 0 {
			b.WriteString("Check out each integration repository at its exact run base before merging dependencies:\n")
			writeOrchestrationCheckoutRefs(&b, task, task.OrchestrationBaseRefs)
			b.WriteString("\n")
		}
		b.WriteString("Dependency commits (merge in this order):\n")
		for _, dependency := range task.OrchestrationDependencies {
			if len(dependency.Heads) > 0 {
				fmt.Fprintf(&b, "- %s:\n", dependency.Key)
				for _, head := range dependency.Heads {
					fmt.Fprintf(&b, "  - repo=%s branch=%s head=%s\n", head.Repo, head.Branch, head.HeadSHA)
				}
			} else {
				fmt.Fprintf(&b, "- %s: branch=%s head=%s\n", dependency.Key, dependency.Branch, dependency.HeadSHA)
			}
		}
		b.WriteString("\nFor remote GitHub resources, use the matching repository URL and merge every listed head into that repository with `git merge --no-ff <sha>`; multi-repo dependencies must be integrated repo by repo. For a local-directory worktree, the repositories are already present and dependency branches live in their shared source repositories. If a commit is unavailable or a conflict cannot be resolved safely, report the concrete blocker instead of completing the step. Finish each repo with `git status --short`, `git log --oneline --decorate -n 12`, and the project checks appropriate to the changed code.\n\n")
	}
	fmt.Fprintf(&b, "Start with `agora issue get %s --output json`. Read comments when needed for human context, but do not use recent-comment scraping as the dependency handoff.\n\n", task.IssueID)
	b.WriteString("If any instruction or answer appears incomplete or ambiguous, first inspect the issue, durable orchestration messages and dependency handoffs, relevant issue comments, and the repository context available in this worktree. Do not guess a product decision, ownership boundary, or coordination contract. If the required decision is still unavailable after that inspection, return outcome `waiting_input` with one precise durable `question` describing the decision, the evidence already checked, and who must answer it.\n\n")
	b.WriteString("Your final response MUST end with exactly one fenced `agora-handoff` JSON object. The server persists this object and injects it directly into dependent stages. Use this shape:\n")
	fmt.Fprintf(&b, "```agora-handoff\n{\"schema_version\":1,\"stage\":%q,\"outcome\":\"completed\",\"verdict\":\"not_applicable\",\"summary\":\"concise outcome\",\"decisions\":[],\"contracts\":[],\"artifacts\":[{\"kind\":\"commit|pr|document|report|deployment\",\"ref\":\"stable reference\",\"description\":\"\"}],\"verification\":[{\"name\":\"check run\",\"status\":\"passed|failed|skipped\",\"details\":\"\"}],\"findings\":[],\"risks\":[],\"blockers\":[],\"next_actions\":[]}\n```\n", task.OrchestrationStage)
	b.WriteString("Use outcome `waiting_input` only when a human decision is required before this same step can continue; then include `question` with `prompt`, `target` set to `human`, and `blocking:true`. Cross-agent context must use the persisted dependency handoff, not a question target. Use outcome `blocked` only for a concrete external blocker and list it in `blockers`. QA and review must set verdict to `pass` or `fail`; other stages use `not_applicable`. Keep any existing stage-specific fenced evidence blocks required by the issue in addition to this final handoff.\n")
	return b.String()
}

type orchestrationContextEntry struct {
	section int
	index   int
	prefix  string
	payload string
}

const (
	orchestrationContextDependency = iota
	orchestrationContextMessage
)

// buildOrchestrationContext selects context by semantic value before restoring
// its stable database order for display. ListOrchestrationStepMessages already
// returns the newest twelve rows in chronological order; selecting newest-first
// here makes the byte bound deterministic without reversing the conversation.
func buildOrchestrationContext(task Task) string {
	if len(task.OrchestrationDependencies) == 0 && len(task.OrchestrationMessages) == 0 {
		return ""
	}

	dependencies := make([]orchestrationContextEntry, 0, len(task.OrchestrationDependencies))
	for i, dependency := range task.OrchestrationDependencies {
		payload := "no structured handoff was recorded"
		if len(dependency.Handoff) > 0 && string(dependency.Handoff) != "null" {
			payload = compactOrchestrationJSON(dependency.Handoff)
		}
		dependencies = append(dependencies, orchestrationContextEntry{
			section: orchestrationContextDependency,
			index:   i,
			prefix:  fmt.Sprintf("- %s: ", dependency.Key),
			payload: payload,
		})
	}

	messages := make([]orchestrationContextEntry, 0, len(task.OrchestrationMessages))
	for i, message := range task.OrchestrationMessages {
		messages = append(messages, orchestrationContextEntry{
			section: orchestrationContextMessage,
			index:   i,
			prefix:  fmt.Sprintf("- %s from %s: ", message.Kind, message.ActorType),
			payload: compactOrchestrationJSON(message.Body),
		})
	}

	fixedBytes := len(orchestrationBoundedContextFooter)
	if len(dependencies) > 0 {
		fixedBytes += len(orchestrationDependencyContextHeader) + len(orchestrationDependencyContextFooter)
	}
	if len(messages) > 0 {
		fixedBytes += len(orchestrationMessageContextHeader) + len(orchestrationMessageContextFooter)
	}
	remaining := orchestrationContextByteBudget - fixedBytes

	selectedDependencies := make([]string, len(dependencies))
	selectedMessages := make([]string, len(messages))
	selected := make(map[[2]int]struct{}, len(dependencies)+len(messages))
	add := func(entry orchestrationContextEntry) {
		key := [2]int{entry.section, entry.index}
		if _, exists := selected[key]; exists || remaining <= len(entry.prefix)+len(orchestrationContextTruncationMarker)+2 {
			return
		}
		entryBudget := min(remaining, orchestrationContextEntryByteBudget)
		rendered := renderOrchestrationContextEntry(entry, entryBudget)
		if rendered == "" {
			return
		}
		selected[key] = struct{}{}
		remaining -= len(rendered)
		if entry.section == orchestrationContextDependency {
			selectedDependencies[entry.index] = rendered
		} else {
			selectedMessages[entry.index] = rendered
		}
	}

	// Protect the continuation's newest clarification exchange first, then
	// the most recent structured stage handoff. These three entries each have
	// their own cap so one pathological payload cannot consume the aggregate.
	for i := len(messages) - 1; i >= 0; i-- {
		if task.OrchestrationMessages[i].Kind == "question" {
			add(messages[i])
			break
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if task.OrchestrationMessages[i].Kind == "answer" {
			add(messages[i])
			break
		}
	}
	for i := len(dependencies) - 1; i >= 0; i-- {
		handoff := task.OrchestrationDependencies[i].Handoff
		if len(handoff) > 0 && string(handoff) != "null" {
			add(dependencies[i])
			break
		}
	}

	// Dependency outputs remain more authoritative than older discussion.
	// Within each class, newest entries win; rendering below restores the
	// persisted order so question/answer causality stays easy to follow.
	for i := len(dependencies) - 1; i >= 0; i-- {
		add(dependencies[i])
	}
	for i := len(messages) - 1; i >= 0; i-- {
		add(messages[i])
	}

	var b strings.Builder
	b.Grow(orchestrationContextByteBudget - remaining)
	if len(dependencies) > 0 {
		b.WriteString(orchestrationDependencyContextHeader)
		for _, rendered := range selectedDependencies {
			b.WriteString(rendered)
		}
		b.WriteString(orchestrationDependencyContextFooter)
	}
	if len(messages) > 0 {
		b.WriteString(orchestrationMessageContextHeader)
		for _, rendered := range selectedMessages {
			b.WriteString(rendered)
		}
		b.WriteString(orchestrationMessageContextFooter)
	}
	b.WriteString(orchestrationBoundedContextFooter)
	return b.String()
}

func renderOrchestrationContextEntry(entry orchestrationContextEntry, budget int) string {
	const newline = "\n"
	full := entry.prefix + entry.payload + newline
	if len(full) <= budget {
		return full
	}

	payloadBudget := budget - len(entry.prefix) - len(orchestrationContextTruncationMarker) - len(newline)
	if payloadBudget <= 0 {
		return ""
	}
	headBudget := payloadBudget * 2 / 3
	tailBudget := payloadBudget - headBudget
	head := truncateUTF8Head(entry.payload, headBudget)
	tail := truncateUTF8Tail(entry.payload, tailBudget)
	return entry.prefix + head + orchestrationContextTruncationMarker + tail + newline
}

func truncateUTF8Head(value string, budget int) string {
	if len(value) <= budget {
		return value
	}
	end := budget
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func truncateUTF8Tail(value string, budget int) string {
	if len(value) <= budget {
		return value
	}
	start := len(value) - budget
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func writeOrchestrationCheckoutRefs(b *strings.Builder, task Task, refs []OrchestrationGitHead) {
	for _, ref := range refs {
		if strings.TrimSpace(ref.HeadSHA) == "" {
			continue
		}
		if repoURL := orchestrationRepoURL(task.Repos, ref.Repo); repoURL != "" {
			fmt.Fprintf(b, "- repo=%s head=%s: `agora repo checkout %q --ref %s`\n", ref.Repo, ref.HeadSHA, repoURL, ref.HeadSHA)
		} else {
			fmt.Fprintf(b, "- repo=%s head=%s: match this repository name to its URL in the runtime brief, then run `agora repo checkout <url> --ref %s`\n", ref.Repo, ref.HeadSHA, ref.HeadSHA)
		}
	}
}

func orchestrationRepoURL(repos []RepoData, refName string) string {
	want := strings.TrimSuffix(strings.TrimSpace(refName), ".git")
	for _, repo := range repos {
		raw := strings.TrimSpace(repo.URL)
		trimmed := strings.TrimSuffix(strings.TrimRight(raw, "/"), ".git")
		separator := strings.LastIndexAny(trimmed, "/:")
		name := trimmed
		if separator >= 0 {
			name = trimmed[separator+1:]
		}
		if raw == refName || strings.EqualFold(name, want) {
			return raw
		}
	}
	return ""
}

func orchestrationStageContract(task Task) string {
	if task.OrchestrationStepKind == "integration" {
		return "Integrate every declared development artifact into one exact result. Record merged artifacts, conflict decisions, final checks, and the exact result the verification stages must inspect. Do not claim completion while any dependency is missing."
	}
	switch task.OrchestrationStage {
	case "plan":
		return "Produce the execution contract: non-overlapping workstream outcomes, cross-workstream interfaces, risks, ownership assumptions, and the checks integration must run. Do not implement the change."
	case "dev":
		return "Implement only this workstream. Record changed contracts, stable artifact references, checks actually run, remaining risks, and what integration must preserve. Leave all repository changes committed."
	case "qa":
		return "Verify the exact integrated artifact without modifying it. Set verdict to pass or fail and record observable evidence for every relevant acceptance criterion, including failed or skipped checks."
	case "review":
		return "Review the exact integrated artifact without modifying it. Set verdict to pass or fail and record correctness, security, maintainability, and regression findings with actionable references."
	case "release":
		return "Act only after the human gate. Release the exact artifact approved by QA and review, verify the destination/reference did not move, and record the final merge or deployment reference."
	default:
		return "Complete only the assigned outcome and leave a structured, verifiable handoff for the next stage."
	}
}

func compactOrchestrationJSON(raw json.RawMessage) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "invalid handoff payload"
	}
	return compact.String()
}

// buildQuickCreatePrompt constructs a prompt for quick-create tasks. The
// user typed a single natural-language sentence in the create-issue modal;
// the agent's job is to translate it into one `agora issue create` CLI
// invocation, using its judgment to decide whether fetching referenced URLs
// would produce a better issue. No issue exists yet, so the agent must NOT
// call `agora issue get` or attempt to comment — there's nothing to read
// or reply to.
func buildQuickCreatePrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a quick-create assistant for a Agora workspace.\n\n")
	b.WriteString("A user captured the following input via the quick-create modal. There is NO existing issue. Your job is to create a well-formed issue from this input with a single `agora issue create` command.\n\n")
	fmt.Fprintf(&b, "User input:\n> %s\n\n", task.QuickCreatePrompt)

	b.WriteString("Field rules:\n\n")

	// title
	b.WriteString("- **title**: required. A concise but semantically rich summary. If the input references external resources (PRs, issues, URLs), use your judgment on whether fetching the resource would produce a meaningfully better title — e.g. \"review PR #123\" → \"Review PR #123: Refactor auth module to OAuth2\". Strip filler words but preserve key semantic information.\n\n")

	// description — the core optimization
	b.WriteString("- **description**: The description is the executing agent's primary context. Aim for high fidelity — they should grasp the user's intent as if they had read the raw input themselves. Use a two-section structure:\n\n")
	b.WriteString("  1. **User request** — Faithfully restate what the user wants in their own words. Preserve specific names, identifiers, file paths, code snippets, and technical terms verbatim. Strip non-spec material before writing it (this is removal, not paraphrasing): verbal routing wrappers about creating the issue or routing it (e.g. \"create an issue\", \"分配给 X\", \"让 @X 处理\") and pure conversational fillers (e.g. \"对吧？\"). When in doubt, keep it.\n\n")
	b.WriteString("     CC exception: `agora issue create` has no `--subscriber` flag, and the platform auto-subscribes members whose `[@Name](mention://member/<uuid>)` link appears in the description. When the user wrote \"cc @Y\", strip the verbal \"cc\" wrapper from the User request body and append a final `CC: <mention link(s)>` line to the description so the cc routing still fires.\n\n")
	b.WriteString("  2. **Context** — include ONLY when the input cited external resources AND you successfully fetched them AND they produced verifiable facts worth recording. Summarize facts only (e.g. \"PR #45 changes auth to JWT\"), not interpretation or unsolicited reference implementations. If you have nothing factual to add, omit the section entirely — never use it as an apology log for resources you could not fetch.\n\n")
	b.WriteString("  Hard rules: never invent requirements, implementation details, or acceptance criteria the user did not express; never reduce multi-sentence input to a single vague sentence; never echo the title.\n\n")

	// priority
	b.WriteString("- **priority**: one of `urgent`, `high`, `medium`, `low`, or omit. Map P0/P1 → urgent/high; \"asap\" → urgent. If unspecified, omit.\n\n")

	// assignee
	b.WriteString("- **assignee**:\n")
	b.WriteString("    - When the user names someone (\"assign to X\" / \"@X\"), call `agora workspace member list --output json`, `agora agent list --output json`, and `agora squad list --output json` and find the matching entity by display name. Squads are first-class assignees too — a squad name (e.g. \"Super Human\") routes work to the squad leader, who then delegates. On a clean unambiguous match, prefer `--assignee-id <uuid>` using the `user_id` (member) or `id` (agent or squad) from that JSON — UUID matching is exact and robust to name collisions in workspaces with overlapping names. `--assignee <name>` (fuzzy) is acceptable as a fallback when names are unambiguous. On no match or ambiguous match, do NOT pass either flag — instead append a final line to the description: `Unrecognized assignee: X`.\n")
	b.WriteString("    - Treat bare @-routing as an assignee directive even when the user did not write the English word \"assign\". This includes Chinese imperatives like `让 @独立团 review 这个 PR`, `给 @X 处理`, or `交给 @X`; strip the leading `@`/`＠` before matching display names. Do not keep that routing wrapper or `@Name` in the description unless it is a true CC-style notification rather than ownership. If the matched entity is a squad, pass the squad's `id` as `--assignee-id`, not the leader agent's id.\n")
	agentID := ""
	agentName := ""
	if task.Agent != nil {
		agentID = task.Agent.ID
		agentName = task.Agent.Name
	}
	switch {
	case task.SquadID != "":
		// The user opened quick-create with a SQUAD selected. The task
		// runs on the squad's leader agent, but the squad is the expected
		// owner — assigning to the leader would mask the squad's
		// delegation flow. Always point the default at the squad UUID.
		if task.SquadName != "" {
			fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to the picker SQUAD %q: pass `--assignee-id %q` (the squad's UUID). The user opened quick-create with the squad selected; you (the leader agent) are running on the squad's behalf, so the squad — not you — is the expected owner. Never leave the issue unassigned, and do not assign it to your own agent UUID.\n\n", task.SquadName, task.SquadID)
		} else {
			fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to the picker SQUAD: pass `--assignee-id %q` (the squad's UUID). The user opened quick-create with the squad selected; you (the leader agent) are running on the squad's behalf, so the squad — not you — is the expected owner. Never leave the issue unassigned, and do not assign it to your own agent UUID.\n\n", task.SquadID)
		}
	case agentID != "":
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee-id %q` (your agent UUID). The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned. Use the UUID flag, not `--assignee <name>`, so the assignment is unambiguous even when other agents share part of your name.\n\n", agentID)
	case agentName != "":
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee %q`. The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned.\n\n", agentName)
	default:
		b.WriteString("    - When the user did NOT name an assignee, default to YOURSELF (the picker agent): pass `--assignee-id <your agent UUID>` (preferred) or `--assignee <your agent name>`. Never leave the issue unassigned.\n\n")
	}

	// project — pinned by the modal when the user picked one, otherwise
	// omitted so the platform routes to the workspace default. Always pass
	// the UUID (never a name) so the issue lands in the right project even
	// when several share a title.
	if task.ProjectID != "" {
		if task.ProjectTitle != "" {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in project %q (the user picked it in the quick-create modal). Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID, task.ProjectTitle)
		} else {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in the project the user picked in the quick-create modal. Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID)
		}
	} else {
		b.WriteString("- **project**: omit. The platform will route the issue to the workspace default.\n")
	}
	// parent — pinned by the modal when the user opened it from "Add sub
	// issue" on an existing issue. Pass the UUID (never the identifier) so
	// the create lands the sub-issue under the right parent even when the
	// workspace prefix changes; the identifier is included in the prose
	// purely as human-readable context for the agent.
	if task.ParentIssueID != "" {
		if task.ParentIssueIdentifier != "" {
			fmt.Fprintf(&b, "- **parent**: required for this run. Pass `--parent %q` so the new issue is filed as a sub-issue of %s (the user opened quick-create from that issue's \"Add sub issue\" entry). Do not infer a different parent from the prompt text — the modal entry point is authoritative.\n", task.ParentIssueID, task.ParentIssueIdentifier)
		} else {
			fmt.Fprintf(&b, "- **parent**: required for this run. Pass `--parent %q` so the new issue is filed as a sub-issue of the parent the user picked in the quick-create modal. Do not infer a different parent from the prompt text — the modal entry point is authoritative.\n", task.ParentIssueID)
		}
	}
	b.WriteString("- **status**: omit (defaults to `todo`).\n")
	b.WriteString("- **attachments**: do NOT pass `--attachment`. The flag only accepts LOCAL file paths. Any image URL in the user input is already markdown — keep it inline in `--description` instead.\n\n")

	// output format
	b.WriteString("Output format:\n")
	b.WriteString("- Run exactly one `agora issue create --output json` invocation. Do not retry for any reason — even on non-zero exit. The issue may already exist; another attempt would create a duplicate.\n")
	b.WriteString("- Parse the JSON response to read the created issue's `identifier` (preferred) or `id` (fallback). Do not scrape human output and do not assume any workspace issue prefix such as `MUL-`; workspaces can use custom prefixes.\n")
	b.WriteString("- After success, print exactly one line: `Created <identifier-or-id>: <title>` and exit. No commentary, no follow-up tool calls.\n")
	b.WriteString("- Do NOT call `agora issue get` or `agora issue comment add` — there is no issue to query or comment on.\n")
	b.WriteString("- On CLI error or JSON parse error, exit with the error as the only output. The platform writes a failure notification automatically.\n")
	return b.String()
}

// buildCommentPrompt constructs a prompt for comment-triggered tasks.
// The triggering comment content is embedded directly so the agent cannot
// miss it, even when stale output files exist in a reused workdir.
// The reply instructions (including the current TriggerCommentID as --parent)
// are re-emitted on every turn so resumed sessions cannot carry forward a
// previous turn's --parent UUID.
func buildCommentPrompt(task Task, provider string) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Agora workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	if task.TriggerCommentContent != "" {
		authorLabel := "A user"
		if task.TriggerAuthorType == "agent" {
			name := task.TriggerAuthorName
			if name == "" {
				name = "another agent"
			}
			authorLabel = fmt.Sprintf("Another agent (%s)", name)
		}
		fmt.Fprintf(&b, "[NEW COMMENT] %s just left a new comment. Focus on THIS comment — do not confuse it with previous ones:\n\n", authorLabel)
		fmt.Fprintf(&b, "> %s\n\n", task.TriggerCommentContent)
		if task.TriggerAuthorType == "agent" {
			b.WriteString("⚠️ The triggering comment was posted by another agent. Decide whether a reply is warranted. If you produced actual work this turn (investigated, fixed something, answered a real question), post the result as a normal reply — that is NOT a noise comment, and the standard rule that final results must be delivered via comment still applies. If the triggering comment was a pure acknowledgment, thanks, or sign-off AND you produced no work this turn, do NOT reply — and do NOT post a comment saying 'No reply needed' or similar. Simply exit with no output. Silence is the preferred way to end agent-to-agent threads. If you do reply, do not @mention the other agent as a sign-off (that re-triggers them and starts a loop).\n\n")
		}
		if task.Agent != nil && strings.Contains(task.Agent.Instructions, "## Squad Operating Protocol") {
			fmt.Fprintf(&b, "⚠️ **Squad leader no_action rule:** If you decide no action is needed, call `agora squad activity %s no_action --reason \"...\"` and EXIT. DO NOT post any comment — not even one that says \"no action needed\" or \"exiting silently\". The squad activity call records your decision; a comment is redundant noise.\n\n", task.IssueID)
		}
	}
	fmt.Fprintf(&b, "Start by running `agora issue get %s --output json` to understand your task, then decide how to proceed.\n\n", task.IssueID)
	// Comment-reading pointer. Warm path with new comments: issue-wide
	// since-delta count, but steer the agent to read the triggering thread
	// first. Warm resumed path with no new comments: the trigger is already
	// injected, so don't force a duplicate thread read. Cold path: read the
	// triggering thread, not the flat timeline. Final fallback (no trigger id,
	// shouldn't happen here): plain read.
	if hint := execenv.BuildNewCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID, task.NewCommentsSince, task.NewCommentCount); hint != "" {
		b.WriteString(hint)
	} else if task.PriorSessionID != "" {
		b.WriteString(execenv.BuildResumedCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID))
	} else if cold := execenv.BuildColdCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID); cold != "" {
		b.WriteString(cold)
	} else {
		fmt.Fprintf(&b, "Read the discussion: `agora issue comment list %s --output json` (long issue? use `--recent 20`).\n\n", task.IssueID)
	}
	b.WriteString(execenv.BuildCommentReplyInstructions(provider, task.IssueID, task.TriggerCommentID))
	return b.String()
}

// buildChatPrompt constructs a prompt for interactive chat tasks.
func buildChatPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a chat assistant for a Agora workspace.\n")
	b.WriteString("A user is chatting with you directly. Respond to their message.\n\n")
	if task.Agent != nil && len(task.Agent.Skills) > 0 {
		refs := ExtractSlashSkills(task.ChatMessage)
		if len(refs) > 0 {
			agentSkills := make(map[string]string, len(task.Agent.Skills))
			for _, s := range task.Agent.Skills {
				agentSkills[s.ID] = s.Name
			}

			selected := make([]string, 0, len(refs))
			seen := make(map[string]struct{}, len(refs))
			for _, ref := range refs {
				name, ok := agentSkills[ref.ID]
				if !ok {
					continue
				}
				if _, ok := seen[ref.ID]; ok {
					continue
				}
				seen[ref.ID] = struct{}{}
				selected = append(selected, name)
			}

			if len(selected) > 0 {
				b.WriteString("Explicitly selected skills:\n")
				for _, name := range selected {
					fmt.Fprintf(&b, "- %s\n", name)
				}
				b.WriteString("\n")
			}
		}
	}
	fmt.Fprintf(&b, "User message:\n%s\n", task.ChatMessage)
	// List attachments by id + filename so the agent can fetch them via
	// the CLI. We deliberately do NOT inline the URL: chat attachments
	// live behind a signed CDN with a short TTL, so by the time the agent
	// has finished thinking the URL embedded in the markdown body may
	// have expired. `agora attachment download <id>` re-signs at click
	// time and is the only reliable path.
	if len(task.ChatMessageAttachments) > 0 {
		b.WriteString("\nAttachments on this message:\n")
		for _, a := range task.ChatMessageAttachments {
			if a.ContentType != "" {
				fmt.Fprintf(&b, "- id=%s filename=%q content_type=%s\n", a.ID, a.Filename, a.ContentType)
			} else {
				fmt.Fprintf(&b, "- id=%s filename=%q\n", a.ID, a.Filename)
			}
		}
		b.WriteString("Use `agora attachment download <id>` to fetch each file locally before referring to it.\n")
		b.WriteString("When creating an issue that should preserve one of these attachments, pass `--attachment-id <id>` to `agora issue create` in addition to keeping the attachment markdown inline.\n")
	}
	return b.String()
}

// buildAutopilotPrompt constructs a prompt for run_only autopilot tasks.
func buildAutopilotPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Agora workspace.\n\n")
	b.WriteString("This task was triggered by an Autopilot in run-only mode. There is no assigned Agora issue for this run.\n\n")
	fmt.Fprintf(&b, "Autopilot run ID: %s\n", task.AutopilotRunID)
	if task.AutopilotID != "" {
		fmt.Fprintf(&b, "Autopilot ID: %s\n", task.AutopilotID)
	}
	if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "Autopilot title: %s\n", task.AutopilotTitle)
	}
	if task.AutopilotSource != "" {
		fmt.Fprintf(&b, "Trigger source: %s\n", task.AutopilotSource)
	}
	if strings.TrimSpace(string(task.AutopilotTriggerPayload)) != "" {
		fmt.Fprintf(&b, "Trigger payload:\n%s\n", strings.TrimSpace(string(task.AutopilotTriggerPayload)))
	}
	b.WriteString("\nAutopilot instructions:\n")
	if strings.TrimSpace(task.AutopilotDescription) != "" {
		b.WriteString(task.AutopilotDescription)
		b.WriteString("\n\n")
	} else if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "%s\n\n", task.AutopilotTitle)
	} else {
		b.WriteString("No additional autopilot instructions were provided. Inspect the autopilot configuration before proceeding.\n\n")
	}
	if task.AutopilotID != "" {
		fmt.Fprintf(&b, "Start by running `agora autopilot get %s --output json` if you need the full autopilot configuration, then complete the instructions above.\n", task.AutopilotID)
	} else {
		b.WriteString("Complete the instructions above.\n")
	}
	b.WriteString("Do not run `agora issue get`; this run does not have an issue ID.\n")
	return b.String()
}
