// Package assistant runs the Agora Assistant — the product's own system-level
// AI. It owns the conversation loop (LLM call → tool call → tool answer → LLM
// call) and nothing else: the tools themselves are executed by a ToolExecutor
// supplied by the HTTP layer, so every tool runs through exactly the same
// membership / visibility guards a request from that user would.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

// Tool names. The catalog is a hard allowlist — a model asking for anything
// not in this list gets an error result, never a fallthrough.
//
// THE MAIN RULE (docs/agora-assistant-plan.md §0): the assistant can do
// everything a user can do manually in Agora. Full UI parity — if a member with
// a given role can click it, the assistant acting for that member can call it.
// Deletes, invites, role changes, workspace settings, automations, autopilot
// workflows and agent configuration are all IN, not out.
//
// The guardrails are the SAME as the UI's, never stricter:
//
//  1. Role permissions are enforced by the REAL handlers (and the real
//     workspace middleware) the write tools run through, so the assistant can
//     never exceed what the caller could do with a browser.
//  2. Destructive and irreversible actions are BOUND to a human confirmation.
//     Calling one of them does not execute it: the executor resolves the
//     target, persists a pending operation describing exactly what would
//     happen, and answers {"status":"needs_confirmation","operation":{…}}. The
//     transcript renders that as a card, and only the user's click on
//     POST /api/assistant/operations/{id}/confirm executes the stored call.
//     DestructiveTools below is that list.
//
// The ONLY carve-out is raw secret VALUES — API keys, tokens, an agent's
// environment variables, MCP auth headers. The conversation transcript is
// persisted, so a secret pasted into it would outlive the chat. Those tools do
// not exist; the assistant points at the Settings page that owns them, which is
// what ExcludedCapabilities below says and the system prompt renders.
//
// Every tool below runs as the requesting human and can see and do exactly
// what they could.
const (
	// Grounding + discovery (read).
	ToolListWorkspaces = "list_workspaces"
	ToolListMyIssues   = "list_my_issues"
	ToolListIssues     = "list_issues"
	ToolSearchIssues   = "search_issues"
	ToolGetIssue       = "get_issue"
	ToolListComments   = "list_comments"
	ToolListProjects   = "list_projects"
	ToolGetProject     = "get_project"
	ToolListSprints    = "list_sprints"
	ToolListLabels     = "list_labels"
	ToolListAgents     = "list_agents"
	ToolListSquads     = "list_squads"
	ToolListMembers    = "list_members"

	// Setting Agora up (read). Grounding for the two setup writes below: an
	// agent must be created on a runtime that already exists, and a skill
	// attachment needs a skill that is already in the library.
	ToolListRuntimes = "list_runtimes"
	ToolListSkills   = "list_skills"

	// Mutations. These go through the same code paths the HTTP handlers use,
	// so every downstream effect (events, task queue, inbox, automations,
	// tracker mirroring) fires exactly as it would for a click in the UI.
	ToolCreateIssue       = "create_issue"
	ToolUpdateIssue       = "update_issue"
	ToolCommentIssue      = "comment_issue"
	ToolArchiveIssue      = "archive_issue"
	ToolAddIssueLabel     = "add_issue_label"
	ToolRemoveIssueLabel  = "remove_issue_label"
	ToolMoveIssueToSprint = "move_issue_to_sprint"
	ToolCreateProject     = "create_project"
	ToolUpdateProject     = "update_project"
	ToolCreateSprint      = "create_sprint"
	ToolCreateLabel       = "create_label"

	// Setting Agora up (write). A workspace with no agent cannot do the thing
	// the product exists for, so "add an agent and give it a skill" is exactly
	// the wrong request to answer with directions to a settings page. These
	// bind to runtimes and skills that already exist — they never touch a
	// credential, pair a machine, or configure MCP.
	ToolCreateAgent        = "create_agent"
	ToolUpdateAgent        = "update_agent"
	ToolAddSkill           = "add_skill"
	ToolAttachSkillToAgent = "attach_skill_to_agent"

	// Deleting. Parity with the ⋯ → Delete menu the UI puts on every one of
	// these — including the dialog: each one parks a pending operation and
	// waits for the user's Confirm click. See DestructiveTools.
	ToolDeleteIssue   = "delete_issue"
	ToolDeleteProject = "delete_project"
	ToolDeleteSprint  = "delete_sprint"
	ToolDeleteLabel   = "delete_label"
	ToolDeleteComment = "delete_comment"

	// People. Settings → Members, in full: invite, change a role, remove. The
	// real handlers run the owner/admin role gate, so a plain member asking for
	// an invite gets the product's own refusal, not a different one.
	ToolInviteMember     = "invite_member"
	ToolUpdateMemberRole = "update_member_role"
	ToolRemoveMember     = "remove_member"

	// Workspaces. Creating one, editing its name/description/context, leaving
	// it, and deleting it (owner-only, and confirmation-bound).
	ToolCreateWorkspace = "create_workspace"
	ToolUpdateWorkspace = "update_workspace"
	ToolLeaveWorkspace  = "leave_workspace"
	ToolDeleteWorkspace = "delete_workspace"

	// Autopilots — recurring work handed to an agent on a schedule.
	ToolListAutopilots  = "list_autopilots"
	ToolCreateAutopilot = "create_autopilot"
	ToolUpdateAutopilot = "update_autopilot"
	ToolRunAutopilotNow = "run_autopilot_now"

	// Automations — the WHEN / IF / THEN rule engine.
	ToolListAutomations      = "list_automations"
	ToolCreateAutomation     = "create_automation"
	ToolSetAutomationEnabled = "set_automation_enabled"
	ToolDeleteAutomation     = "delete_automation"

	// Integrations (read). What this workspace is actually wired to — GitHub,
	// git accounts, MCP servers, Figma, Release, Bitrix, Zoho, Telegram, Lark
	// — with each connector's status and the page that owns it.
	//
	// Read-ONLY, and that is the whole design. Every one of those connectors is
	// completed by pasting a credential, and the one thing this assistant never
	// touches is a secret value (see ExcludedCapabilities). So the catalog gives
	// it perfect sight and no hands: it can say what is connected, what is not,
	// and what to do next — and the last step happens in Settings, where the
	// value goes straight into an owner-gated endpoint instead of into a
	// transcript that is stored forever.
	ToolListIntegrations = "list_integrations"

	// The small parity items: the things a user does with one click that the
	// assistant previously had to describe instead of doing.
	ToolMarkInboxRead  = "mark_inbox_read"
	ToolUpdateComment  = "update_comment"
	ToolResolveComment = "resolve_comment"
	ToolPinItem        = "pin_item"
	ToolSubscribeIssue = "subscribe_issue"

	// The user's OWN settings. Parity with Settings → Preferences and
	// Settings → Notifications: language, timezone, display name, which
	// sidebar items they hide, and which notification groups are muted in a
	// workspace. These are USER-scoped, not workspace-scoped (except the
	// notification preferences, which are per workspace), and they are
	// reversible in one further call — so they are neither confirmation-bound
	// nor role-gated beyond the membership the workspace one already needs.
	ToolGetMySettings                 = "get_my_settings"
	ToolUpdateMySettings              = "update_my_settings"
	ToolUpdateSidebar                 = "update_sidebar"
	ToolUpdateNotificationPreferences = "update_notification_preferences"

	// Analytics (read).
	ToolUsageSummary   = "usage_summary"
	ToolActivityDigest = "activity_digest"
	ToolInboxSummary   = "inbox_summary"
	ToolQAStatus       = "qa_status"

	// Artifacts — rich outputs (chart, table, report, small HTML tool) that
	// live beside the transcript instead of inside it. These are the only
	// tools in the catalog scoped to the SESSION rather than to a workspace:
	// an artifact belongs to the conversation that produced it and may
	// aggregate numbers from every workspace the user belongs to.
	ToolCreateArtifact = "create_artifact"
	ToolUpdateArtifact = "update_artifact"

	// Plans — several related writes proposed as ONE thing to authorize.
	// Everyday management work (planning a sprint, triaging an inbox, moving a
	// dozen stale issues back to todo) is N writes, and the product's "one
	// request, one write" rule makes the assistant safer than clicking but
	// slower than it. A plan keeps the safety and removes the slowness: the
	// model proposes the whole ordered list, the human reads it as one card,
	// unchecks what they do not want, and presses Confirm once. See
	// PlanAllowedTools for what may ride in one.
	ToolProposePlan = "propose_plan"
)

// MutatingTools is the set of tools that write. Kept explicit so the run loop
// and any future confirmation gate can ask "is this a write?" without
// string-matching names at the call site.
var MutatingTools = map[string]bool{
	ToolCreateIssue:          true,
	ToolUpdateIssue:          true,
	ToolCommentIssue:         true,
	ToolArchiveIssue:         true,
	ToolAddIssueLabel:        true,
	ToolRemoveIssueLabel:     true,
	ToolMoveIssueToSprint:    true,
	ToolCreateProject:        true,
	ToolUpdateProject:        true,
	ToolCreateSprint:         true,
	ToolCreateLabel:          true,
	ToolCreateAgent:          true,
	ToolUpdateAgent:          true,
	ToolAddSkill:             true,
	ToolAttachSkillToAgent:   true,
	ToolCreateArtifact:       true,
	ToolUpdateArtifact:       true,
	ToolDeleteIssue:          true,
	ToolDeleteProject:        true,
	ToolDeleteSprint:         true,
	ToolDeleteLabel:          true,
	ToolDeleteComment:        true,
	ToolInviteMember:         true,
	ToolUpdateMemberRole:     true,
	ToolRemoveMember:         true,
	ToolCreateWorkspace:      true,
	ToolUpdateWorkspace:      true,
	ToolLeaveWorkspace:       true,
	ToolDeleteWorkspace:      true,
	ToolCreateAutopilot:      true,
	ToolUpdateAutopilot:      true,
	ToolRunAutopilotNow:      true,
	ToolCreateAutomation:     true,
	ToolSetAutomationEnabled: true,
	ToolDeleteAutomation:     true,
	ToolMarkInboxRead:        true,
	ToolUpdateComment:        true,
	ToolResolveComment:       true,
	ToolPinItem:              true,
	ToolSubscribeIssue:       true,
	// Personal settings. Writes, but deliberately NOT in DestructiveTools:
	// every one of them is undone by calling the same tool with the other
	// value, and binding a confirmation card to "mute comments" is how a
	// confirmation gate stops meaning anything.
	ToolUpdateMySettings:              true,
	ToolUpdateSidebar:                 true,
	ToolUpdateNotificationPreferences: true,
	// A plan mutates NOTHING when it is called — it persists a proposal and
	// waits for a click, exactly as a destructive tool does. It counts as a
	// write here for the same reason those do: the run loop files an execution
	// receipt for every mutating call, and a proposal that later moves a dozen
	// issues must be in that record from the moment it was made.
	ToolProposePlan: true,
}

// IsMutating reports whether a tool writes.
func IsMutating(name string) bool { return MutatingTools[name] }

// DestructiveTools is the confirmation-binding list: every tool here destroys
// something the user cannot get back from the conversation, or removes
// somebody's access.
//
// This is the chat equivalent of the UI's confirm dialog, and it exists for the
// same reason: in a browser, "delete" is two gestures — the menu item and the
// dialog — and the second one is where the user reads what is about to happen.
//
// The gesture is an OUT-OF-BAND HUMAN CLICK, not an argument. A model-supplied
// `confirm: true` was the earlier design and it is not evidence of anything: a
// model that hallucinates a delete, or reads "delete it" out of an issue body
// (prompt injection), writes that boolean just as readily as an honest one, and
// a user's typed "yes" is a string in a transcript the model also writes into.
// So a call to one of these tools persists a pending operation, resolved
// against real target data, and returns needs_confirmation. Execution happens
// only when POST /api/assistant/operations/{id}/confirm arrives from the
// session's owner — an authenticated request the model cannot forge.
//
// Membership here is about UNDOABILITY, not about the verb. archive_issue,
// remove_issue_label and update_issue are reversible in one further call and
// are deliberately NOT bound — binding them would train the user to click
// Confirm on everything, which is how a confirmation gate stops working.
var DestructiveTools = map[string]bool{
	ToolDeleteIssue:      true,
	ToolDeleteProject:    true,
	ToolDeleteSprint:     true,
	ToolDeleteLabel:      true,
	ToolDeleteComment:    true,
	ToolRemoveMember:     true,
	ToolLeaveWorkspace:   true,
	ToolDeleteWorkspace:  true,
	ToolDeleteAutomation: true,
}

// RequiresConfirmation reports whether a tool must be bound to a human
// confirmation before it may execute.
func RequiresConfirmation(name string) bool { return DestructiveTools[name] }

// ---------------------------------------------------------------------------
// Plans
// ---------------------------------------------------------------------------

// MaxPlanItems is the hard cap on one plan.
//
// It is a READABILITY limit before it is a safety limit: the card is a list a
// person has to read before pressing one button, and nobody audits forty rows.
// A bigger job is proposed in slices, which also gives the user a place to stop.
const MaxPlanItems = 25

// MaxPlanTitleLength bounds the plan's headline — it is the summary of the
// pending row and the heading of the card, not a description.
const MaxPlanTitleLength = 200

// PlanAllowedTools is what may ride inside a plan, and it is deliberately a
// SHORTER list than the catalog.
//
// One confirmation authorizing N calls is a weaker gesture than N
// confirmations: the user reads a list, not a sentence, and the failure mode of
// a list is skimming it. So a plan may only carry the everyday, recoverable
// work — issue / label / sprint / project writes and the inbox read flag. What
// is NOT here is as much of the design as what is:
//
//   - no deletes (they are DestructiveTools and each one owes the user its own
//     card, naming the one thing it destroys),
//   - no member, workspace, agent, skill, automation or autopilot writes —
//     access changes and standing machinery are not bulk work,
//   - no settings, no artifacts.
//
// The list is enforced at PROPOSE time and re-checked at CONFIRM time, so an
// allowlist that shrinks between the two invalidates the stale items rather
// than executing them.
var PlanAllowedTools = map[string]bool{
	ToolCreateIssue:       true,
	ToolUpdateIssue:       true,
	ToolArchiveIssue:      true,
	ToolAddIssueLabel:     true,
	ToolRemoveIssueLabel:  true,
	ToolCommentIssue:      true,
	ToolMoveIssueToSprint: true,
	ToolCreateLabel:       true,
	ToolCreateSprint:      true,
	ToolCreateProject:     true,
	ToolUpdateProject:     true,
	ToolSubscribeIssue:    true,
	ToolMarkInboxRead:     true,
	ToolResolveComment:    true,
}

// PlanAllows reports whether a tool may appear as a plan item.
func PlanAllows(name string) bool { return PlanAllowedTools[name] }

// IsCatalogTool reports whether a name is a tool the executor can actually run.
//
// The allowlist alone is not enough: it is a hand-written map, and a plan item
// naming a tool that is in it but no longer in the catalog would pass propose
// and fail at execution, after the user had authorized it.
func IsCatalogTool(name string) bool {
	for _, spec := range ToolSpecs() {
		if spec.Name == name {
			return true
		}
	}
	return false
}

// PlanItem is one call inside a plan: the tool, the arguments it would run
// with, and the line the human reads before authorizing the whole list.
type PlanItem struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Summary   string          `json:"summary"`
}

// Plan is a propose_plan argument blob after validation. It is what gets
// persisted as the pending operation's arguments, so the confirmed execution
// replays THESE items and never anything said afterwards.
type Plan struct {
	Title string     `json:"title"`
	Items []PlanItem `json:"items"`
}

// ParsePlan validates a propose_plan argument blob.
//
// Every failure here is returned to the MODEL as a tool error, which is the
// correction it needs: a plan that names a tool plans may not carry is a plan
// the model can rewrite, and telling it exactly which item and which tool is
// what makes the retry cheap.
func ParsePlan(raw json.RawMessage) (Plan, error) {
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return Plan{}, errors.New("could not read the plan as JSON: " + err.Error())
	}
	plan.Title = strings.TrimSpace(plan.Title)
	if plan.Title == "" {
		return Plan{}, errors.New("a plan needs a title — one line naming what the whole plan does")
	}
	if len(plan.Title) > MaxPlanTitleLength {
		return Plan{}, fmt.Errorf("the plan title is %d characters; keep it under %d", len(plan.Title), MaxPlanTitleLength)
	}
	if len(plan.Items) == 0 {
		return Plan{}, errors.New("a plan needs at least one item")
	}
	if len(plan.Items) > MaxPlanItems {
		return Plan{}, fmt.Errorf("a plan carries at most %d items; this one has %d — propose the first %d and say what is left",
			MaxPlanItems, len(plan.Items), MaxPlanItems)
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		item.Tool = strings.TrimSpace(item.Tool)
		item.Summary = strings.TrimSpace(item.Summary)
		if item.Tool == "" {
			return Plan{}, fmt.Errorf("item %d names no tool", i)
		}
		if !PlanAllows(item.Tool) {
			return Plan{}, fmt.Errorf("item %d uses %s, which cannot go in a plan — plans carry only %s. Ask for that one on its own",
				i, item.Tool, strings.Join(PlanAllowedToolNames(), ", "))
		}
		if !IsCatalogTool(item.Tool) {
			return Plan{}, fmt.Errorf("item %d uses %s, which is not a tool", i, item.Tool)
		}
		if item.Summary == "" {
			return Plan{}, fmt.Errorf("item %d has no summary — that line is what the user reads before authorizing it", i)
		}
		// A JSON OBJECT, not a string of JSON and not a list: the arguments are
		// replayed verbatim into the executor, which decodes them exactly as it
		// decodes a direct call's.
		var args map[string]json.RawMessage
		if len(item.Arguments) == 0 || json.Unmarshal(item.Arguments, &args) != nil {
			return Plan{}, fmt.Errorf("item %d must carry an arguments object for %s", i, item.Tool)
		}
	}
	return plan, nil
}

// PlanAllowedToolNames is the allowlist in a stable order, for the messages
// and the system prompt.
func PlanAllowedToolNames() []string {
	names := make([]string, 0, len(PlanAllowedTools))
	for name := range PlanAllowedTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Tool-result statuses in the confirmation/receipt wire contract
// (docs/agora-assistant-final-plan.md, "Pinned wire contract"). They are
// constants here because both the executor that writes them and the transcript
// renderer that switches on them must agree, and a typo on either side degrades
// silently into "the model saw a blob it did not understand".
const (
	// StatusNeedsConfirmation is the answer to a destructive call with no bound
	// confirmation: a pending operation was persisted and NOTHING was mutated.
	// It rides with an "operation" object — {id, tool_name, summary,
	// workspace_slug, target{type,identifier,title}}.
	StatusNeedsConfirmation = "needs_confirmation"
	// StatusUncertain is a mutation whose request was DISPATCHED and whose
	// outcome could not be established (the run's deadline landed mid-flight,
	// say). It rides with "inspect": what to go and look at. It is never an
	// error, because an error invites a retry and a retry may double the effect.
	StatusUncertain = "uncertain"
)

// ExcludedCapability is one thing a user CAN do in the product and the
// assistant deliberately CANNOT. It carries the place in the app that does own
// the action, because the only acceptable refusal is a specific one: "that is
// intentionally not something I can do — it lives in Settings → Members".
//
// This list is rendered into the system prompt, so it is documentation and
// behaviour at once: adding a capability here teaches the model to decline it
// correctly, and a tool whose name collides with one of these is caught by the
// catalog test.
type ExcludedCapability struct {
	// Capability is what the user asked for, in their words.
	Capability string
	// Where is the exact UI location that owns it.
	Where string
	// Why is the one-line reason it is not a tool. Kept out of the prompt
	// (the model does not need to argue the policy) but recorded here so the
	// next person to widen the catalog knows which line they are crossing.
	Why string
}

// ExcludedCapabilities is the standing "no" list.
//
// It used to be long — deletes, invites, workspace settings, automations,
// autopilots, billing. The MAIN RULE (docs/agora-assistant-plan.md §0) deleted
// all of that: full UI parity means the assistant does whatever the user's role
// lets them do, with the product's own confirm dialogs mirrored as the pending
// operation + ConfirmCard that DestructiveTools produces.
//
// What is left is ONE entry, and it is not a capability gap — it is a channel
// gap. The assistant can still create the agent, still point at the settings
// page, still explain what to paste; it just never carries the secret VALUE,
// because the conversation is stored and a key typed into it would outlive the
// chat, the session, and the user's memory of having typed it.
//
// Anything added here from now on has to clear that same bar: not "this feels
// risky", but "the transcript is the wrong place for this value to exist".
var ExcludedCapabilities = []ExcludedCapability{
	{
		Capability: "Accepting a raw secret VALUE — an API key, an access token, an agent's environment variables, or an MCP auth header. " +
			"Everything AROUND the secret is available: creating the agent, the runtime list, and telling the user exactly where to paste it",
		Where: "Settings → AI Accounts for provider keys, Settings → MCP for MCP credentials, " +
			"Settings → Runtimes to pair a machine, and an agent's own Environment tab for its variables",
		Why: "the chat transcript is persisted, so a secret pasted into it outlives the conversation; " +
			"each of these values has its own owner/admin-gated, audit-logged endpoint and must reach it directly",
	},
}

// Result bounds. Every fan-out tool caps per workspace, because a user in a
// dozen workspaces would otherwise blow the context window on one call.
const (
	// MaxToolResultIssues bounds issue lists (per workspace when unscoped).
	MaxToolResultIssues = 20
	// MaxActivityRows bounds the activity digest per workspace.
	MaxActivityRows = 50
	// MaxInboxRows bounds the inbox summary across ALL workspaces — it is one
	// list the user reads top-down, not a per-workspace rollup.
	MaxInboxRows = 30
	// MaxUsageRangeDays / MaxDigestDays cap the analytics windows.
	MaxUsageRangeDays = 90
	MaxDigestDays     = 30
)

// ToolExecutor runs one allowlisted tool as the given user, inside the given
// session. It is defined here and implemented in the handler package (which
// owns the queries and the visibility gates) so the service never imports the
// handler — that would be a cycle, since the handler holds the service.
//
// sessionID is an explicit parameter rather than a value smuggled through ctx
// because it is an INPUT to the artifact tools, not request-scoped plumbing: a
// create_artifact that cannot see its session has nothing to write to, and a
// compiler error at the call site is the only way to guarantee a future
// executor cannot silently lose it. Every other tool ignores it.
//
// An executor returns an error only for conditions the MODEL should see and can
// correct (unknown tool, bad arguments, no access). The run loop turns those
// into {"error": "..."} tool results and keeps going; it never aborts a run
// because one tool failed.
type ToolExecutor interface {
	Execute(ctx context.Context, userID, sessionID, name string, args json.RawMessage) (json.RawMessage, error)
}

// ToolSpecs is the catalog handed to the model on every round.
//
// The schemas are deliberately tight (additionalProperties:false, explicit
// maxima): the free tier model is a weaker tool-caller than Claude, and a
// closed schema is the cheapest way to keep its arguments inside what the
// executor can actually satisfy.
func ToolSpecs() []llm.Tool {
	return []llm.Tool{
		{
			Name: ToolListWorkspaces,
			Description: "List every workspace the user is a member of, with the user's role in each. " +
				"Call this first whenever you need a workspace_id, or when the user asks a question that spans " +
				"more than one workspace.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`),
		},
		{
			Name: ToolListMyIssues,
			Description: "List the issues assigned to the user. Omit workspace_id to fan out across EVERY workspace " +
				"the user belongs to (bounded per workspace) — that is how to answer \"what is on my plate\". " +
				"Pass workspace_id to look at one workspace only. " +
				"The result carries a scope object naming every workspace checked, any that could not be read, " +
				"whether the list was truncated, and the exact total — quote those, never the row count.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {
      "type": "string",
      "description": "UUID of one workspace, from list_workspaces. Omit to search every workspace the user belongs to."
    },
    "status": {
      "type": "string",
      "description": "Optional status filter, e.g. todo, in_progress, in_review, done, cancelled."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 50,
      "description": "Maximum issues to return (per workspace when unscoped). Defaults to 20."
    }
  },
  "additionalProperties": false
}`),
		},
		{
			Name: ToolListIssues,
			Description: "List the issues in ONE workspace — everybody's, not just the user's. This is the plain " +
				"issue list the board and the Issues page show, and it is what to COUNT from: " +
				"\"how many bugs are open\", \"what is in the Platform project\", \"show me everything in review\". " +
				"list_my_issues answers a narrower question (only the user's own) and will under-count if you " +
				"use it for a workspace-wide total. " +
				"scope.total is an exact count taken with the same filters (archived issues excluded unless " +
				"include_archived), so it is the number to quote even when the rows are capped.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace, from list_workspaces."},
    "status": {
      "type": "string",
      "enum": ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"],
      "description": "Optional status filter."
    },
    "priority": {
      "type": "string",
      "enum": ["urgent", "high", "medium", "low", "none"],
      "description": "Optional priority filter."
    },
    "project_id": {"type": "string", "description": "Optional project to list: a UUID from list_projects, or the project title."},
    "assignee_id": {"type": "string", "description": "Optional assignee UUID — a member from list_members, an agent from list_agents, or a squad from list_squads."},
    "include_archived": {
      "type": "boolean",
      "description": "Include archived issues, which are hidden by default. Defaults to false."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 50,
      "description": "Maximum issues to return. Defaults to 20."
    }
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolSearchIssues,
			Description: "Full-text search issues in ONE workspace by title, description and comments. " +
				"Use it to find an issue the user described in words rather than by identifier. " +
				"Closed issues are excluded unless include_closed is true.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {
      "type": "string",
      "description": "UUID of the workspace to search, from list_workspaces."
    },
    "query": {
      "type": "string",
      "description": "Search phrase. May also be an issue identifier such as MUL-123."
    },
    "include_closed": {
      "type": "boolean",
      "description": "Include done and cancelled issues. Defaults to false."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 50,
      "description": "Maximum issues to return. Defaults to 20."
    }
  },
  "required": ["workspace_id", "query"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolGetIssue,
			Description: "Read one issue in full: description, status, priority, assignee, dates. " +
				"`ref` is an identifier like MUL-123 or an issue UUID. Use it after search_issues or " +
				"list_my_issues to look at a specific issue before answering or changing it.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."}
  },
  "required": ["workspace_id", "ref"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolListComments,
			Description: "Read the comment thread on one issue, oldest to newest. Use it before answering " +
				"\"what did they decide on MUL-12\" or before replying, so your comment continues the " +
				"discussion instead of repeating it.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 50,
      "description": "How many of the most recent comments to read. Defaults to 20."
    }
  },
  "required": ["workspace_id", "ref"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolListProjects,
			Description: "List the projects in a workspace. Use it to turn a project name the user said " +
				"into the project_id create_issue needs.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolGetProject,
			Description: "Read one project: description, status, priority, lead, squad binding and issue counts. " +
				"`project` may be the project UUID or its title, so \"the TEST project\" resolves without a " +
				"separate lookup. Call it before update_project so you change the project the user meant.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace, from list_workspaces."},
    "project": {"type": "string", "description": "Project UUID, or the project's title (case-insensitive)."}
  },
  "required": ["workspace_id", "project"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolListSprints,
			Description: "List the sprints in a workspace, or in one project. Use it to turn a sprint name " +
				"the user said into the sprint_id move_issue_to_sprint needs.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace, from list_workspaces."},
    "project_id": {"type": "string", "description": "Optional project UUID or project title. Omit to list every sprint in the workspace."}
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolListLabels,
			Description: "List the labels defined in a workspace. Use it to turn a label name into the " +
				"label add_issue_label / remove_issue_label take, and to check whether a label already " +
				"exists before creating a new one.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolListAgents,
			Description: "List the AI agents in a workspace (only the ones this user may see). " +
				"Use it to turn an agent name into the assignee_id needed to assign work to an agent.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolListSquads,
			Description: "List the squads (teams of agents) in a workspace. Assigning an issue to a squad " +
				"is what triggers decomposition and delegation, so use this to turn a squad name into the " +
				"assignee_id for assignee_type \"squad\".",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolListMembers,
			Description: "List the people in a workspace with their roles. Use it to turn a person's name " +
				"into the assignee_id needed to assign work to a teammate.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolListRuntimes,
			Description: "List the agent runtimes connected to a workspace — the machines and cloud nodes " +
				"agents actually run on. Every agent must be created on one of these, so call this before " +
				"create_agent. An EMPTY list means the workspace has no runtime connected yet and no agent " +
				"can be created until someone connects one.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolListSkills,
			Description: "List the skills in a workspace's library — the reusable instruction packs agents " +
				"can be given. Pass agent_id to list what ONE agent already has instead. Use it before " +
				"add_skill (is it already here?) and before attach_skill_to_agent (which id do I attach?).",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace, from list_workspaces."},
    "agent_id": {"type": "string", "description": "Optional agent UUID or agent name. When given, lists that agent's skills instead of the whole library."}
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolListAutopilots,
			Description: "List the workspace's autopilots — recurring work handed to an agent, either on a cron " +
				"schedule or fired by a webhook. Each row carries its status (active / paused / archived) and its " +
				"schedule, so this answers \"what runs automatically\" and grounds the id the update and run tools take.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolListAutomations,
			Description: "List the workspace's automations — WHEN/IF/THEN rules that react to issue events " +
				"(status changed, label attached, comment posted). The result also carries the CATALOG of valid " +
				"trigger types, step types and operators, so read it before calling create_automation rather " +
				"than guessing a trigger name.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolListIntegrations,
			Description: "List every third-party connector for a workspace with its real status: GitHub, git " +
				"accounts, MCP servers, Release, Figma, Bitrix24, Zoho, Telegram and Lark. READ-ONLY. " +
				"Call this FIRST whenever the user asks whether something is connected, asks you to connect " +
				"or set up a tool, or reports an integration not working — never answer that from memory. " +
				"Each row carries status (connected / not_connected / unavailable / unknown), a short " +
				"non-secret detail, and `where`: the exact place in the app that owns it. " +
				"\"unavailable\" means the instance operator has not enabled that connector, so no amount of " +
				"clicking in Settings will help — say who can fix it. It returns NO tokens, auth headers or " +
				"URLs, and there is no tool for setting one: the user pastes credentials at `where`.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolCreateIssue,
			Description: "Create a new issue. WRITE — only call it when the user has actually asked for an " +
				"issue to be created, and never to 'try something out'. Ground project_id and assignee_id with " +
				"list_projects / list_agents / list_members first; never invent an id.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to create the issue in."},
    "title": {"type": "string", "description": "Short issue title, in the user's language."},
    "description": {"type": "string", "description": "Optional Markdown body."},
    "priority": {
      "type": "string",
      "enum": ["urgent", "high", "medium", "low", "none"],
      "description": "Defaults to none."
    },
    "status": {
      "type": "string",
      "enum": ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"],
      "description": "Defaults to todo."
    },
    "project_id": {"type": "string", "description": "Optional project to file the issue under: a UUID from list_projects, or the project title."},
    "assignee_type": {
      "type": "string",
      "enum": ["member", "agent", "squad"],
      "description": "Must be sent together with assignee_id."
    },
    "assignee_id": {"type": "string", "description": "UUID of the member, agent or squad. Must be sent together with assignee_type."},
    "due_date": {"type": "string", "description": "Optional due date as YYYY-MM-DD."}
  },
  "required": ["workspace_id", "title"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateIssue,
			Description: "Change fields on an existing issue: title, body, status, priority, assignee, dates, " +
				"which project it belongs to, and its parent issue. WRITE — only the fields you pass are " +
				"touched. Resolve the issue with search_issues or get_issue first so you are certain which " +
				"one the user means, and ground project_id with list_projects or get_project.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "title": {"type": "string", "description": "New title. Rewrites the title outright — do not send it unless the user asked for a rename."},
    "description": {"type": "string", "description": "New Markdown body. REPLACES the existing body; read it with get_issue first if the user asked to add to it."},
    "status": {
      "type": "string",
      "enum": ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"]
    },
    "priority": {
      "type": "string",
      "enum": ["urgent", "high", "medium", "low", "none"]
    },
    "assignee_type": {
      "type": "string",
      "enum": ["member", "agent", "squad"],
      "description": "Must be sent together with assignee_id."
    },
    "assignee_id": {"type": "string", "description": "UUID of the new assignee. Must be sent together with assignee_type."},
    "project_id": {"type": "string", "description": "The project to move the issue into — a UUID from list_projects or the project title — or an empty string to take it out of its project."},
    "parent_issue_id": {"type": "string", "description": "The issue that becomes the parent, as an identifier (MUL-123) or UUID, or an empty string to detach from the current parent."},
    "due_date": {"type": "string", "description": "Due date as YYYY-MM-DD, or an empty string to clear it."}
  },
  "required": ["workspace_id", "ref"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCommentIssue,
			Description: "Post a comment on an issue as the user. WRITE — the comment is public to the team " +
				"and can trigger assigned agents, so only post what the user asked you to post.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "body": {"type": "string", "description": "Comment text in Markdown, in the user's language."}
  },
  "required": ["workspace_id", "ref", "body"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolArchiveIssue,
			Description: "Archive an issue, or bring an archived one back. WRITE — archiving hides the issue " +
				"from lists and boards WITHOUT deleting it, so it is the right answer when the user wants an " +
				"issue \"gone\" or \"cleaned up\". It is fully reversible: call it again with archived=false.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "archived": {
      "type": "boolean",
      "description": "true archives the issue (the default), false restores it."
    }
  },
  "required": ["workspace_id", "ref"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolAddIssueLabel,
			Description: "Put a label on an issue. WRITE — `label` may be the label's UUID or its exact name; " +
				"the name must already exist, so call list_labels first and create_label if it does not.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "label": {"type": "string", "description": "Label UUID, or the label's name (case-insensitive) from list_labels."}
  },
  "required": ["workspace_id", "ref", "label"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolRemoveIssueLabel,
			Description: "Take a label off an issue. WRITE — this only detaches the label from this one issue; " +
				"the label itself keeps existing in the workspace. get_issue lists the labels an issue has.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "label": {"type": "string", "description": "Label UUID, or the label's name (case-insensitive)."}
  },
  "required": ["workspace_id", "ref", "label"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolMoveIssueToSprint,
			Description: "Put an issue into a sprint, or take it out of the one it is in. WRITE — `sprint` may " +
				"be the sprint UUID or its name from list_sprints; send an empty string to remove the issue " +
				"from its sprint. A sprint is project-scoped, so the issue must belong to the sprint's project.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "sprint": {"type": "string", "description": "Sprint UUID or sprint name from list_sprints. Empty string removes the issue from its current sprint."}
  },
  "required": ["workspace_id", "ref", "sprint"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCreateProject,
			Description: "Create a project — the container issues and sprints live in. WRITE — check " +
				"list_projects first so you do not create a second project with a name the workspace " +
				"already has. Ground lead_id with list_members / list_agents and squad_id with list_squads.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to create the project in."},
    "title": {"type": "string", "description": "Project title, in the user's language."},
    "description": {"type": "string", "description": "Optional description."},
    "status": {
      "type": "string",
      "enum": ["planned", "in_progress", "paused", "completed", "cancelled"],
      "description": "Defaults to planned."
    },
    "priority": {
      "type": "string",
      "enum": ["urgent", "high", "medium", "low", "none"],
      "description": "Defaults to none."
    },
    "lead_type": {
      "type": "string",
      "enum": ["member", "agent"],
      "description": "Must be sent together with lead_id."
    },
    "lead_id": {"type": "string", "description": "UUID of the member or agent leading the project. Must be sent together with lead_type."},
    "squad_id": {"type": "string", "description": "Optional squad UUID from list_squads. Binds the project so only that squad may work its issues."}
  },
  "required": ["workspace_id", "title"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateProject,
			Description: "Change fields on an existing project. WRITE — only the fields you pass are touched; " +
				"everything you leave out keeps its current value. `project` may be the project UUID or its " +
				"title. Read it with get_project first when the user named it in words.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the project lives in."},
    "project": {"type": "string", "description": "Project UUID, or the project's title (case-insensitive)."},
    "title": {"type": "string", "description": "New title."},
    "description": {"type": "string", "description": "New description. REPLACES the existing one; an empty string clears it."},
    "status": {
      "type": "string",
      "enum": ["planned", "in_progress", "paused", "completed", "cancelled"]
    },
    "priority": {
      "type": "string",
      "enum": ["urgent", "high", "medium", "low", "none"]
    },
    "lead_type": {
      "type": "string",
      "enum": ["member", "agent"],
      "description": "Must be sent together with lead_id."
    },
    "lead_id": {"type": "string", "description": "UUID of the new lead. Must be sent together with lead_type."},
    "squad_id": {"type": "string", "description": "Squad UUID from list_squads, or an empty string to unbind the project from its squad."}
  },
  "required": ["workspace_id", "project"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCreateSprint,
			Description: "Create a sprint inside a project. WRITE — sprints are project-scoped, so ground " +
				"project_id with list_projects or get_project first. Dates are optional; a sprint with no " +
				"dates is a plain bucket of work.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the project lives in."},
    "project_id": {"type": "string", "description": "The project this sprint belongs to: a UUID from list_projects, or the project title."},
    "name": {"type": "string", "description": "Sprint name, e.g. \"Sprint 12\"."},
    "goal": {"type": "string", "description": "Optional one-line goal for the sprint."},
    "status": {
      "type": "string",
      "enum": ["planned", "active", "completed"],
      "description": "Defaults to planned."
    },
    "start_date": {"type": "string", "description": "Optional start date as YYYY-MM-DD."},
    "end_date": {"type": "string", "description": "Optional end date as YYYY-MM-DD."}
  },
  "required": ["workspace_id", "project_id", "name"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCreateLabel,
			Description: "Create a workspace label. WRITE — call list_labels first: label names are unique " +
				"per workspace and re-creating an existing one fails. Only create a label when the user " +
				"asked for a new one, or when they asked to tag an issue with a label that does not exist yet.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to create the label in."},
    "name": {"type": "string", "description": "Label name, e.g. \"bug\"."},
    "color": {"type": "string", "description": "Optional 6-digit hex color such as #2563eb. A neutral color is used when omitted."}
  },
  "required": ["workspace_id", "name"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolDeleteIssue,
			Description: "Permanently delete an issue, with its comments and attachments. DESTRUCTIVE AND " +
				"IRREVERSIBLE — there is no undo and no trash. Prefer archive_issue when the user just wants it " +
				"out of the way. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."}
  },
  "required": ["workspace_id", "ref"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolDeleteProject,
			Description: "Permanently delete a project. DESTRUCTIVE AND IRREVERSIBLE — its sprints and " +
				"knowledge items go with it, and its issues are detached from it. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the project lives in."},
    "project": {"type": "string", "description": "Project UUID, or the project's title (case-insensitive)."}
  },
  "required": ["workspace_id", "project"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolDeleteSprint,
			Description: "Permanently delete a sprint. DESTRUCTIVE AND IRREVERSIBLE — the issues survive, but " +
				"they lose this sprint and its history. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the sprint lives in."},
    "sprint": {"type": "string", "description": "Sprint UUID or sprint name from list_sprints."}
  },
  "required": ["workspace_id", "sprint"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolDeleteLabel,
			Description: "Permanently delete a label from the workspace, taking it off every issue that carries " +
				"it. DESTRUCTIVE AND IRREVERSIBLE. To take a label off ONE issue use remove_issue_label instead — " +
				"that is the reversible one. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the label lives in."},
    "label": {"type": "string", "description": "Label UUID, or the label's name (case-insensitive)."}
  },
  "required": ["workspace_id", "label"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolDeleteComment,
			Description: "Permanently delete one comment. DESTRUCTIVE AND IRREVERSIBLE. Only the comment's " +
				"author, or a workspace owner/admin, may delete it — anyone else is refused. Get the comment_id " +
				"from list_comments. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the comment lives in."},
    "comment_id": {"type": "string", "description": "Comment UUID, from list_comments."}
  },
  "required": ["workspace_id", "comment_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateComment,
			Description: "Edit the text of a comment. WRITE — the new body REPLACES the old one, so read the " +
				"thread with list_comments first when the user asked to add something. Only the author or a " +
				"workspace owner/admin may edit a comment.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the comment lives in."},
    "comment_id": {"type": "string", "description": "Comment UUID, from list_comments."},
    "body": {"type": "string", "description": "The complete new comment text, in Markdown."}
  },
  "required": ["workspace_id", "comment_id", "body"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolResolveComment,
			Description: "Mark a comment thread resolved, or reopen it. WRITE and fully reversible — call it " +
				"again with resolved=false to reopen. An issue has at most one resolved comment, so resolving " +
				"one clears any other resolution on the same issue.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the comment lives in."},
    "comment_id": {"type": "string", "description": "Comment UUID, from list_comments."},
    "resolved": {"type": "boolean", "description": "true resolves the thread (the default), false reopens it."}
  },
  "required": ["workspace_id", "comment_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolMarkInboxRead,
			Description: "Mark the user's inbox items read in one workspace — everything at once, or one item. " +
				"WRITE, but a harmless one: it clears the unread badge and changes nothing about the work. " +
				"Call inbox_summary first when the user wants to know what is in there.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace whose inbox to mark, from list_workspaces."},
    "item_id": {"type": "string", "description": "Optional inbox item UUID. Omit to mark every unread item in that workspace read."}
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolPinItem,
			Description: "Pin an issue or a project to the user's own sidebar, or unpin it. WRITE and fully " +
				"reversible; the pin is private to this user and changes nothing for the team.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the item lives in."},
    "item_type": {
      "type": "string",
      "enum": ["issue", "project"],
      "description": "What is being pinned."
    },
    "item": {"type": "string", "description": "For an issue: its identifier (MUL-123) or UUID. For a project: its title or UUID."},
    "pinned": {"type": "boolean", "description": "true pins it (the default), false unpins it."}
  },
  "required": ["workspace_id", "item_type", "item"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolSubscribeIssue,
			Description: "Subscribe the user to an issue so its activity reaches their inbox, or unsubscribe " +
				"them. WRITE and fully reversible. This only ever changes the CALLING user's own subscription.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the issue lives in."},
    "ref": {"type": "string", "description": "Issue identifier (MUL-123) or issue UUID."},
    "subscribed": {"type": "boolean", "description": "true subscribes (the default), false unsubscribes."}
  },
  "required": ["workspace_id", "ref"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolInviteMember,
			Description: "Invite somebody to the workspace by email. WRITE and OUTWARD-FACING — it sends them " +
				"mail and, once accepted, grants access to the workspace's work. Only a workspace owner or admin " +
				"may invite; anyone else gets the product's own refusal. Check list_members first so you do not " +
				"invite somebody who is already in.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to invite into."},
    "email": {"type": "string", "description": "The invitee's email address, exactly as the user gave it."},
    "role": {
      "type": "string",
      "enum": ["admin", "member"],
      "description": "Role to grant on accept. Defaults to member. Nobody can be invited as owner."
    }
  },
  "required": ["workspace_id", "email"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateMemberRole,
			Description: "Change a teammate's role in the workspace. WRITE — a role change adds or removes " +
				"access to settings, members and billing. Only an owner or admin may change roles, and only an " +
				"owner may grant or revoke ownership. Ground user_id with list_members.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace."},
    "user_id": {"type": "string", "description": "The teammate's user_id, from list_members."},
    "role": {
      "type": "string",
      "enum": ["owner", "admin", "member"],
      "description": "The new role."
    }
  },
  "required": ["workspace_id", "user_id", "role"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolRemoveMember,
			Description: "Remove somebody from the workspace. DESTRUCTIVE — they lose access immediately, and " +
				"their runtimes and tokens in this workspace are revoked. Only an owner or admin may remove a " +
				"member. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace."},
    "user_id": {"type": "string", "description": "The teammate's user_id, from list_members."}
  },
  "required": ["workspace_id", "user_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCreateWorkspace,
			Description: "Create a new workspace, with this user as its owner. WRITE — say what you created and " +
				"quote the slug so the user can open it. The slug is the URL segment: lowercase letters, digits " +
				"and hyphens only. Omit it to have one derived from the name.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "Workspace name, in the user's language."},
    "slug": {"type": "string", "description": "Optional URL slug — lowercase letters, digits and hyphens. Derived from the name when omitted."},
    "description": {"type": "string", "description": "Optional one-line description."},
    "issue_prefix": {"type": "string", "description": "Optional issue identifier prefix, e.g. MUL for MUL-123. Derived from the name when omitted."}
  },
  "required": ["name"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateWorkspace,
			Description: "Change a workspace's name, description or context. WRITE — only the fields you pass " +
				"are touched. `context` is the standing background every agent in the workspace is given, so " +
				"rewriting it changes how every agent behaves: read it back to the user before replacing it. " +
				"Only an owner or admin may edit a workspace.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to edit."},
    "name": {"type": "string", "description": "New name."},
    "description": {"type": "string", "description": "New description. REPLACES the existing one; an empty string clears it."},
    "context": {"type": "string", "description": "New workspace context for agents. REPLACES the existing one."}
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolLeaveWorkspace,
			Description: "Leave a workspace the user is a member of. DESTRUCTIVE for them — they lose access to " +
				"everything in it, and the last owner cannot leave. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to leave."}
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolDeleteWorkspace,
			Description: "Permanently delete a whole workspace — every issue, project, agent and member in it. " +
				"THE MOST DESTRUCTIVE ACTION IN THE PRODUCT, and it cannot be undone. Owner-only. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to delete."}
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCreateAgent,
			Description: "Create an AI agent in a workspace. WRITE — an agent must run on a runtime that is " +
				"already connected, so call list_runtimes FIRST and pass one of its ids. If list_runtimes " +
				"comes back empty, say that the workspace has no runtime connected yet and that connecting " +
				"one is done in Settings → Runtimes; do not guess an id. The agent is created owned by this " +
				"user, with the same defaults a create form leaves alone.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to create the agent in."},
    "name": {"type": "string", "description": "Agent name, unique within the workspace."},
    "description": {"type": "string", "description": "Optional one-line description of what this agent is for."},
    "instructions": {"type": "string", "description": "Optional standing instructions the agent follows on every task."},
    "runtime_id": {"type": "string", "description": "UUID of the runtime to run on, from list_runtimes. Required."},
    "model": {"type": "string", "description": "Optional model name for this agent. Omit to use the runtime's default."}
  },
  "required": ["workspace_id", "name", "runtime_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateAgent,
			Description: "Configure an agent: its name, description, standing instructions, model, who can see " +
				"it, how many tasks it runs at once, and whether it is archived. WRITE — only the fields you " +
				"pass are touched. Its environment variables and MCP config are the one thing NOT here: those " +
				"carry secrets and are set in the agent's own Environment tab. Only the agent's owner or a " +
				"workspace owner/admin may edit an agent; anyone else is refused.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the agent lives in."},
    "agent_id": {"type": "string", "description": "Agent UUID, or the agent's name from list_agents."},
    "name": {"type": "string", "description": "New name."},
    "description": {"type": "string", "description": "New description. REPLACES the existing one."},
    "instructions": {"type": "string", "description": "New standing instructions. REPLACES the existing ones — read them with list_agents context first if the user asked to add to them."},
    "model": {"type": "string", "description": "New model name."},
    "visibility": {
      "type": "string",
      "enum": ["workspace", "private"],
      "description": "workspace = everyone in the workspace can see and assign it; private = only its owner and workspace admins."
    },
    "max_concurrent_tasks": {
      "type": "integer",
      "minimum": 1,
      "maximum": 50,
      "description": "How many tasks this agent may run at the same time."
    },
    "archived": {
      "type": "boolean",
      "description": "true archives the agent and cancels its pending tasks; false restores it. Reversible either way."
    }
  },
  "required": ["workspace_id", "agent_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolAddSkill,
			Description: "Fetch a skill from a public source (GitHub, ClawHub or skills.sh link) into the " +
				"workspace's skill library. WRITE — check list_skills first; a skill that is already there " +
				"is reported as skipped rather than duplicated. Adding a skill to the library does NOT give " +
				"it to any agent: follow with attach_skill_to_agent.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to add the skill to."},
    "url": {"type": "string", "description": "Link to the skill on GitHub, ClawHub or skills.sh."},
    "on_conflict": {
      "type": "string",
      "enum": ["skip", "rename", "overwrite", "fail"],
      "description": "What to do when a skill of that name already exists. Defaults to skip."
    }
  },
  "required": ["workspace_id", "url"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolAttachSkillToAgent,
			Description: "Give one agent a skill from the workspace library. WRITE — additive: the agent " +
				"keeps every skill it already has. Ground both ids with list_agents and list_skills; " +
				"list_skills with agent_id shows what the agent already has.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace both belong to."},
    "agent_id": {"type": "string", "description": "Agent UUID, or the agent's name."},
    "skill_id": {"type": "string", "description": "Skill UUID, or the skill's name from list_skills."}
  },
  "required": ["workspace_id", "agent_id", "skill_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCreateAutopilot,
			Description: "Create an autopilot — standing work an agent does again and again, on a schedule. " +
				"WRITE, and it KEEPS FIRING after the conversation ends, so only create one the user actually " +
				"asked for and say plainly when it will run. `prompt` is the brief the agent is given every " +
				"time. Ground assignee_id with list_agents or list_squads. Pass cron_expression to give it a " +
				"schedule; without one it only runs when somebody triggers it.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to create the autopilot in."},
    "title": {"type": "string", "description": "Short name, e.g. \"Daily standup digest\"."},
    "prompt": {"type": "string", "description": "The brief the agent is given on every run. This is the autopilot's description."},
    "assignee_type": {
      "type": "string",
      "enum": ["agent", "squad"],
      "description": "Defaults to agent. Must match assignee_id."
    },
    "assignee_id": {"type": "string", "description": "UUID of the agent (list_agents) or squad (list_squads) that runs it."},
    "project_id": {"type": "string", "description": "Optional project to file created issues under: a UUID from list_projects, or the project title."},
    "execution_mode": {
      "type": "string",
      "enum": ["create_issue", "run_only"],
      "description": "create_issue (the default) opens an issue each run; run_only dispatches the agent without one."
    },
    "issue_title_template": {"type": "string", "description": "Optional title pattern for the issue each run creates."},
    "cron_expression": {"type": "string", "description": "Optional 5-field cron, e.g. \"0 9 * * 1-5\" for weekdays at 09:00. Omit for a manual-only autopilot."},
    "timezone": {"type": "string", "description": "IANA timezone for cron_expression, e.g. Asia/Tashkent. Defaults to UTC."}
  },
  "required": ["workspace_id", "title", "assignee_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateAutopilot,
			Description: "Change an autopilot: pause or resume it, rewrite its prompt, re-point it at another " +
				"agent, or change its schedule. WRITE — only the fields you pass are touched. " +
				"status=paused is how you turn one OFF without losing it; cron_expression re-schedules it.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the autopilot lives in."},
    "autopilot": {"type": "string", "description": "Autopilot UUID, or its title from list_autopilots."},
    "title": {"type": "string", "description": "New title."},
    "prompt": {"type": "string", "description": "New brief for every run. REPLACES the existing one."},
    "status": {
      "type": "string",
      "enum": ["active", "paused", "archived"],
      "description": "active runs it, paused stops it firing without deleting it, archived retires it."
    },
    "assignee_type": {
      "type": "string",
      "enum": ["agent", "squad"],
      "description": "Must be sent together with assignee_id."
    },
    "assignee_id": {"type": "string", "description": "UUID of the new agent or squad. Must be sent together with assignee_type."},
    "project_id": {"type": "string", "description": "Project for created issues: a UUID or title, or an empty string to unset it."},
    "execution_mode": {
      "type": "string",
      "enum": ["create_issue", "run_only"]
    },
    "issue_title_template": {"type": "string", "description": "New title pattern for the issue each run creates."},
    "cron_expression": {"type": "string", "description": "New 5-field cron. Creates a schedule if the autopilot had none."},
    "timezone": {"type": "string", "description": "IANA timezone for cron_expression."}
  },
  "required": ["workspace_id", "autopilot"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolRunAutopilotNow,
			Description: "Run an autopilot immediately, once, without changing its schedule. WRITE — this " +
				"actually dispatches the agent, so only do it when the user asked for a run now. The autopilot " +
				"must be active; a paused one is refused.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the autopilot lives in."},
    "autopilot": {"type": "string", "description": "Autopilot UUID, or its title from list_autopilots."}
  },
  "required": ["workspace_id", "autopilot"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolCreateAutomation,
			Description: "Create an automation — a WHEN/IF/THEN rule that reacts to issue events. WRITE, and it " +
				"KEEPS FIRING long after the conversation, so build only what the user asked for and read the " +
				"rule back to them afterwards. CALL list_automations FIRST: its result carries the catalog of " +
				"valid trigger types, step types, operators and fields, and `conditions` / `actions` here take " +
				"exactly the JSON the automations editor sends.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace to create the automation in."},
    "name": {"type": "string", "description": "Short name for the rule, in the user's language."},
    "description": {"type": "string", "description": "Optional one-line explanation of what it does."},
    "trigger_type": {"type": "string", "description": "WHEN: one of the trigger types list_automations returns, e.g. issue.status_changed."},
    "trigger_config": {"type": "string", "description": "Optional JSON object of trigger options, e.g. {\"to\":\"in_review\"}. Send \"{}\" or omit when there are none."},
    "conditions": {"type": "string", "description": "IF: a JSON ARRAY of {\"field\":..,\"op\":..,\"value\":..} clauses, ALL of which must hold. Send \"[]\" for a rule with no conditions."},
    "actions": {"type": "string", "description": "THEN: a JSON ARRAY of steps, e.g. [{\"type\":\"add_label\",\"config\":{\"name\":\"needs-qa\"}}]. Every config value is a STRING. At least one step is required."},
    "project_id": {"type": "string", "description": "Optional project to scope the rule to: a UUID from list_projects, or the project title. Omit to apply it workspace-wide."},
    "enabled": {"type": "boolean", "description": "Whether it starts switched on. Defaults to true."}
  },
  "required": ["workspace_id", "name", "trigger_type", "actions"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolSetAutomationEnabled,
			Description: "Switch an automation on or off without changing or losing it. WRITE and fully " +
				"reversible — this is the right answer to \"stop that rule\", not delete_automation.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the automation lives in."},
    "automation": {"type": "string", "description": "Automation UUID, or its name from list_automations."},
    "enabled": {"type": "boolean", "description": "true switches it on, false switches it off."}
  },
  "required": ["workspace_id", "automation", "enabled"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolDeleteAutomation,
			Description: "Permanently delete an automation and its run history. DESTRUCTIVE AND IRREVERSIBLE — " +
				"prefer set_automation_enabled(false) when the user only wants it to stop. " +
				"Calling this changes nothing by itself — it returns a confirmation card, and the action runs only when the user presses Confirm on it. Say what would happen and that you are waiting for their click; never report it as done.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace the automation lives in."},
    "automation": {"type": "string", "description": "Automation UUID, or its name from list_automations."}
  },
  "required": ["workspace_id", "automation"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolGetMySettings,
			Description: "Read the user's OWN settings in one call: display name, email, interface language, " +
				"pinned timezone, which sidebar items they have hidden, and — for one workspace — their " +
				"notification preferences. READ-ONLY. Call this before changing a setting so you can tell " +
				"them what it was before. Notification preferences are per workspace: pass workspace_id to " +
				"read a specific one, or omit it to read the workspace this message was sent from. " +
				"A notification group missing from the map is on its default, which is \"all\".",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {
      "type": "string",
      "description": "UUID of the workspace whose notification preferences to read, from list_workspaces. Omit for the workspace this message was sent from."
    }
  },
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateMySettings,
			Description: "Change the user's own profile settings: interface language, pinned timezone, or " +
				"display name. WRITE, reversible, and personal — it affects only the calling user, in every " +
				"workspace. Send only the fields that change; the others are left alone. Say plainly what " +
				"you changed and what it was before. Passing an empty timezone clears the pin and goes back " +
				"to following the browser's timezone.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "language": {
      "type": "string",
      "enum": ["en", "zh-Hans", "uz", "ru"],
      "description": "Interface language."
    },
    "timezone": {
      "type": "string",
      "description": "IANA timezone name, e.g. Asia/Tashkent. Empty string clears the pin and follows the browser again."
    },
    "name": {"type": "string", "description": "Display name shown to the rest of the team."}
  },
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateSidebar,
			Description: "Hide or restore items in the user's sidebar — the same switches Settings → " +
				"Preferences offers. WRITE, reversible, personal, and MERGED against what they already " +
				"hide: naming one item never disturbs the rest. Use the nav keys get_my_settings reports " +
				"(inbox, issues, projects, autopilots, automations, agents, squads, usage, runtimes, " +
				"skills, plugins, mcp, artifacts, assistant). Settings itself can never be hidden — it is " +
				"the only way back to this screen — and asking to hide it comes back as a refusal.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "hide": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Nav keys to hide. Added to whatever the user already hides."
    },
    "show": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Nav keys to bring back. Removed from the hidden list; keys that were not hidden are ignored."
    }
  },
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateNotificationPreferences,
			Description: "Mute or unmute a notification group for the user in ONE workspace — the switches " +
				"Settings → Notifications offers. WRITE, reversible, and personal: it changes only the " +
				"calling user's own notifications, never anybody else's. MERGED against their current " +
				"settings, so naming one group leaves the others alone. \"all\" means notify, \"muted\" " +
				"means do not. system_notifications is the desktop banner toggle rather than an inbox group.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace, from list_workspaces."},
    "preferences": {
      "type": "object",
      "properties": {
        "assignments": {"type": "string", "enum": ["all", "muted"], "description": "Issues assigned to the user."},
        "status_changes": {"type": "string", "enum": ["all", "muted"], "description": "Status changes on issues they follow."},
        "comments": {"type": "string", "enum": ["all", "muted"], "description": "Comments and mentions."},
        "updates": {"type": "string", "enum": ["all", "muted"], "description": "Other issue updates."},
        "agent_activity": {"type": "string", "enum": ["all", "muted"], "description": "What the agents did."},
        "system_notifications": {"type": "string", "enum": ["all", "muted"], "description": "Native desktop notification banners."}
      },
      "additionalProperties": false,
      "description": "The groups to change. Groups you leave out keep their current value."
    }
  },
  "required": ["workspace_id", "preferences"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUsageSummary,
			Description: "Token spend and task counts for a workspace over the last N days, broken down by " +
				"day and by agent. Answers \"how much did we burn this week\". Token counts only — this " +
				"instance does not price them server-side.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "UUID of the workspace, from list_workspaces."},
    "range_days": {
      "type": "integer",
      "minimum": 1,
      "maximum": 90,
      "description": "Window in days. Defaults to 30."
    }
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolActivityDigest,
			Description: "Recent recorded activity — what changed and who changed it. Omit workspace_id to " +
				"roll up across every workspace the user belongs to. Answers \"what happened this week\".",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "Optional. Omit to roll up across all the user's workspaces."},
    "since_days": {
      "type": "integer",
      "minimum": 1,
      "maximum": 30,
      "description": "How far back to look. Defaults to 7."
    }
  },
  "additionalProperties": false
}`),
		},
		{
			Name: ToolInboxSummary,
			Description: "The user's unread inbox items across every workspace, newest first. " +
				"Answers \"what needs my attention\".",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`),
		},
		{
			Name: ToolQAStatus,
			Description: "QA regression totals for a workspace over the last 30 days: how many test runs " +
				"passed, failed and were skipped, plus how much of the suite is scripted.",
			Parameters: workspaceOnlySchema("UUID of the workspace, from list_workspaces."),
		},
		{
			Name: ToolCreateArtifact,
			Description: "Produce an ARTIFACT — a chart, table, report or small interactive page that opens " +
				"in its own pane beside the conversation instead of being typed out as chat text. " +
				"Use it whenever the user asks for a chart, graph, dashboard, report, comparison table or any " +
				"visualization. Ground the data FIRST with the read/analytics tools and build the artifact from " +
				"the numbers they returned — never from numbers you assembled yourself. " +
				"Prefer chart, table or markdown; use html only when the answer genuinely needs interactivity. " +
				"To change an artifact you already made, call update_artifact — do not create a second one.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "description": "Short label shown on the artifact card, e.g. \"Agent usage by day\"."
    },
    "kind": {
      "type": "string",
      "enum": ["chart", "table", "markdown", "html"],
      "description": "chart = JSON spec rendered as a native chart; table = JSON spec rendered as a table; markdown = a report or brief; html = ONE self-contained HTML document, only when interactivity is genuinely needed."
    },
    "content": {
      "type": "string",
      "description": "For chart: JSON {\"type\":\"bar|line|area|pie\",\"x\":\"<row field for the category axis>\",\"series\":[{\"key\":\"<row field>\",\"label\":\"<legend label>\"}],\"rows\":[{\"<x field>\":\"Mon\",\"<series key>\":12}]}. For table: JSON {\"columns\":[\"A\",\"B\"],\"rows\":[[\"a\",1]]}. For markdown: the markdown text. For html: the whole document, CSS and JS inline."
    }
  },
  "required": ["title", "kind", "content"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolUpdateArtifact,
			Description: "Replace the content of an artifact you already produced in this conversation, bumping " +
				"its version. This is the right tool for every follow-up on something already on screen " +
				"(\"add the QA numbers\", \"make it a line chart\", \"only the last 7 days\") — send the FULL new " +
				"content, it replaces the old body rather than appending to it. The kind cannot change. " +
				"Every version is kept, so the user can go back to an earlier one.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "artifact_id": {
      "type": "string",
      "description": "UUID of the artifact, from the create_artifact or update_artifact result earlier in this conversation."
    },
    "content": {
      "type": "string",
      "description": "The complete replacement body, in the same format the artifact's kind requires."
    },
    "title": {
      "type": "string",
      "description": "Optional new label. Omit to keep the title the user already sees."
    },
    "expected_version": {
      "type": "integer",
      "description": "Optional concurrency check: the version number you are editing, from the create_artifact or update_artifact result you are building on. If the artifact has moved on since then the update is refused and tells you its current version, instead of overwriting a change you never saw. Pass it whenever you are rewriting a body you read earlier."
    }
  },
  "required": ["artifact_id", "content"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolProposePlan,
			Description: "Propose SEVERAL related writes as ONE thing the user authorizes. Use it whenever a " +
				"request implies more than one write — planning a sprint, moving a set of issues, triaging an " +
				"inbox, setting a project up: do the reads first, then send ONE plan instead of a chain of " +
				"single calls. Calling this changes NOTHING: it returns a confirmation card listing the items, " +
				"the user unchecks any they do not want and presses Confirm once, and only then do the items " +
				"run, in order, stopping at the first failure. " +
				"Each item's summary is the line the human reads before authorizing it, so name the real target " +
				"— the identifier and title you got from a read, never a placeholder. " +
				"Only these tools may go in a plan: " + strings.Join(PlanAllowedToolNames(), ", ") + ". " +
				"Anything else (deletes, members, workspaces, agents, automations, settings) is asked for on " +
				"its own. At most " + fmt.Sprint(MaxPlanItems) + " items — for a bigger job, propose the first " +
				"slice and say what is left.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "maxLength": 200,
      "description": "One line naming what the whole plan does, e.g. \"Plan sprint 12: 6 issues from the backlog\"."
    },
    "items": {
      "type": "array",
      "minItems": 1,
      "maxItems": 25,
      "description": "The calls to run, in the order they should run.",
      "items": {
        "type": "object",
        "properties": {
          "tool": {
            "type": "string",
            "description": "Name of an allowlisted tool, e.g. update_issue."
          },
          "arguments": {
            "type": "object",
            "description": "The exact arguments that tool would be called with, as an object — the same shape as calling it directly."
          },
          "summary": {
            "type": "string",
            "description": "The human-readable line for this row, naming the real target, e.g. \"Move MUL-142 “Login loops” back to todo\"."
          }
        },
        "required": ["tool", "arguments", "summary"],
        "additionalProperties": false
      }
    }
  },
  "required": ["title", "items"],
  "additionalProperties": false
}`),
		},
	}
}

// workspaceOnlySchema is the schema shared by every tool whose only argument is
// a required workspace id. Written once so the four of them cannot drift.
func workspaceOnlySchema(description string) json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "` + description + `"}
  },
  "required": ["workspace_id"],
  "additionalProperties": false
}`)
}
