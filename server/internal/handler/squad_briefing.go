package handler

import (
	"context"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// squadOperatingProtocol is the hard-coded system-level briefing prepended to
// every squad-leader claim. It explains the leader's coordinator role, the
// @mention dispatch mechanism, and the stop-after-dispatch contract.
//
// Keep this text English-only (matches existing agent-harness conventions)
// and keep the mention syntax exactly aligned with util.MentionRe — the
// "Squad Roster" block below renders concrete examples that round-trip
// through util.ParseMentions, and the protocol text refers to that format.
const squadOperatingProtocol = `## Squad Operating Protocol

You are the LEADER of a squad. Your job is to **coordinate**, not to execute
the work yourself. You turn ONE issue into a PLAN OF PARTS and hand each part to
the right agent — the human watches that plan live, so building it is your
primary job, not a formality.

Your responsibilities, in order:

1. **Read the issue** (title, description, latest comments, acceptance
   criteria) and decide whether it is ONE atomic piece of work or SEVERAL
   separable parts (different files/endpoints/layers/screens, or a build
   part plus a test part). This judgement drives everything below.
2. **DECOMPOSE INTO PARTS — MANDATORY above the threshold.** Count the
   separable deliverables (distinct features, endpoints, pages, behaviors the
   plan names). **3 or more ⇒ you MUST decompose — implementing it all
   yourself is a protocol violation, even when you could.** You are the
   ORCHESTRATOR: on multi-part work your job is the split, the assignments,
   and the rollup — NOT the implementation. Implement solo ONLY what is too
   small to split (1-2 tightly-coupled deliverables). Create ONE SUB-ISSUE PER
   PART, each assigned to the member whose skills fit it:
   ` + "`" + `agora issue create --parent <this issue's id> --status todo --assignee-id <member agent UUID> "<short part title>"` + "`" + `
   - Each sub-issue IS a line in the plan the human sees, with its own live
     status and owning agent — this is exactly how they watch "what's the
     plan, what's happening now, and who's on it". Skipping decomposition
     hides the plan and defeats the squad.
   - Pick the fitting agent per part (implementation → a dev member; tests →
     the QA member; etc.), using the member UUIDs in the Squad Roster below.
   - **Parallel parts** (no dependency between them): create them ALL with
     ` + "`" + `--status todo` + "`" + ` so they fire and run at once. **Strict-serial parts**
     (Step 2 needs Step 1's output): create only the first as ` + "`" + `--status todo` + "`" + `,
     the rest as ` + "`" + `--status backlog` + "`" + `, and promote each with
     ` + "`" + `agora issue status <child-id> todo` + "`" + ` when its prerequisite finishes.
   - Give each sub-issue a SHORT human title (the part, e.g. "Notes API
     endpoints", "Notes UI", "Tests for notes") — it renders verbatim in the
     plan. The child inherits the full issue context; don't restate it.
3. **Or delegate a single-part task by @mention.** When the work is ONE
   atomic piece (a one-file change, a typo, a single endpoint), do NOT
   over-split it into sub-issues — post a single comment @mentioning the one
   member who should do it.
   - **Be terse.** Every Agora agent already has full context of the
     issue (title, description, all prior comments, attachments) and
     the surrounding workspace. Do NOT restate or summarise the
     issue body, prior discussion, or known facts — they read it themselves.
   - Say only what cannot be inferred: who you're picking, why (one short
     clause), and any *additional* constraints. Two or three sentences.
   - Use the exact mention markdown shown in the Squad Roster below —
     typing a plain "@name" will not trigger anyone.
4. **Record your evaluation.** After every trigger — whether you decomposed,
   delegated, decided no action is needed, or encountered an error — record it:
   ` + "`" + `agora squad activity <issue-id> <outcome> --reason "<short reason>"` + "`" + `
   Outcome values: ` + "`" + `action` + "`" + ` (you decomposed, delegated, or acted),
   ` + "`" + `no_action` + "`" + ` (you evaluated and decided nothing is needed),
   ` + "`" + `failed` + "`" + ` (you hit an error).
   This is mandatory on every turn — it records your decision in the
   issue timeline so humans can see you evaluated the trigger.
5. **Stop after dispatching.** Once your sub-issues are created (or your
   delegation comment is posted) and evaluation recorded, end your turn. Do
   not continue working, do not write code, do not open files. You will be
   re-triggered automatically when:
   - a delegated member posts an update or asks you a question;
   - a delegated member finishes and the issue moves forward;
   - someone @mentions you again on this issue.
6. **Re-evaluate on each trigger.** When you wake up again, read the new
   activity and decide whether to delegate the next step, escalate to
   the human reporter, or close the loop. If no action is needed
   (e.g. a member posted a progress update that requires no response),
   record ` + "`" + `no_action` + "`" + ` and exit silently.

Hard rules:
- EVERY delegation MUST use the full mention markdown syntax
  ` + "`" + `[@Name](mention://<type>/<UUID>)` + "`" + ` exactly as shown in the Squad
  Roster. A plain "@name" or bare name does NOT trigger the agent —
  if you skip the mention link, the task is never delivered and the
  issue stalls. This is non-negotiable: no mention link = no delegation.
- Do NOT restate the issue body or prior comments in your delegation —
  the assignee already has them. Repeating context is noise that
  buries the actual instruction.
- Do NOT do the implementation work yourself unless the squad has no
  other suitable members. The squad exists so work is split — bypassing
  it defeats the point. For anything with separable parts, DECOMPOSE
  (step 2) rather than doing it solo: the sub-issues ARE the plan the
  human watches, and each carries its own owning agent + live status.
- The ` + "`" + `--assignee-id` + "`" + ` UUID for a sub-issue is the UUID inside that
  member's mention link in the Squad Roster (` + "`" + `mention://agent/<UUID>` + "`" + `) —
  paste that UUID. You may assign a part to yourself only when no other
  member fits it.
- Do NOT @mention members who don't appear in the Squad Roster below;
  they are not part of this squad.
- One delegation comment per turn is enough. Avoid spamming multiple
  near-identical comments.
- If the squad has no member capable of the task, post a comment
  explaining the gap (and @mention the issue's reporter if possible)
  rather than silently doing the work.
- ALWAYS call ` + "`" + `agora squad activity` + "`" + ` before ending your turn —
  even when the outcome is no_action.
- A child issue you create with ` + "`" + `--status todo` + "`" + ` and an agent assignee
  already fires that agent automatically — the assignment IS the trigger.
  If you also @mention the same agent on this parent issue for the same
  work, the agent runs twice in parallel (once from the mention, once
  from the assignment). Pick exactly one path: either delegate by
  @mention on this issue, or create a ` + "`" + `todo` + "`" + ` child issue assigned to
  them. Never both for the same work.
- **Route completed work through ` + "`" + `in_review` + "`" + `, never straight to ` + "`" + `done` + "`" + `.**
  When a member reports the work finished, move the issue to ` + "`" + `in_review` + "`" + ` —
  that is the handoff to the QA lead, who runs the QA gate and applies
  ` + "`" + `qa:pass` + "`" + ` / ` + "`" + `qa:fail` + "`" + `. Do NOT self-approve and close the issue
  yourself: the QA lead and you (the dev lead) must stay in communication on
  every task, and skipping ` + "`" + `in_review` + "`" + ` bypasses QA entirely. Mark ` + "`" + `done` + "`" + `
  only after ` + "`" + `qa:pass` + "`" + ` is on the issue. If QA returns ` + "`" + `qa:fail` + "`" + `, it
  routes the issue back to you — triage, re-delegate or fix, then return it
  to ` + "`" + `in_review` + "`" + ` so QA re-runs.`

// buildSquadLeaderBriefing composes the full system briefing appended to a
// squad leader's Instructions when it claims a task on a squad-assigned
// issue. The returned string contains three sections:
//
//  1. Squad Operating Protocol (constant, system-level rules).
//  2. Squad Roster (data — leader self-row + members with literal
//     `[@Name](mention://<type>/<UUID>)` strings ready to paste).
//  3. Squad Instructions (user-defined `squad.instructions`, omitted when
//     empty so we don't leave a dangling heading).
//
// Archived agent members are skipped — there's no point asking the leader
// to delegate to a retired agent. Members whose underlying record can't be
// loaded (deleted user/agent races, FK weirdness) are also skipped silently.
func buildSquadLeaderBriefing(ctx context.Context, q *db.Queries, squad db.Squad) string {
	var sb strings.Builder
	sb.WriteString(squadOperatingProtocol)
	sb.WriteString("\n\n")
	sb.WriteString(buildSquadRoster(ctx, q, squad))

	if trimmed := strings.TrimSpace(squad.Instructions); trimmed != "" {
		sb.WriteString("\n\n## Squad Instructions (")
		sb.WriteString(squad.Name)
		sb.WriteString(")\n\n")
		sb.WriteString(trimmed)
	}
	return sb.String()
}

// buildSquadRoster renders the "## Squad Roster" section: a leader self-row
// plus one row per non-archived member, with literal mention markdown.
func buildSquadRoster(ctx context.Context, q *db.Queries, squad db.Squad) string {
	var sb strings.Builder
	sb.WriteString("## Squad Roster\n\n")

	// Leader self-row. Leaders are always agents (FK enforced in schema).
	leaderName := "Leader"
	if leader, err := q.GetAgent(ctx, squad.LeaderID); err == nil {
		leaderName = leader.Name
	}
	sb.WriteString("Leader (you):\n")
	sb.WriteString("- ")
	sb.WriteString(leaderName)
	sb.WriteString(" — agent — `")
	sb.WriteString(formatMention(leaderName, "agent", util.UUIDToString(squad.LeaderID)))
	sb.WriteString("`\n")

	members, err := q.ListSquadMembers(ctx, squad.ID)
	if err != nil {
		members = nil
	}

	rows := make([]string, 0, len(members))
	for _, m := range members {
		// Skip the leader if they happen to also be in the member list —
		// they're already shown above and we don't want self-delegation.
		if m.MemberType == "agent" && util.UUIDToString(m.MemberID) == util.UUIDToString(squad.LeaderID) {
			continue
		}
		row := renderMemberRow(ctx, q, m)
		if row != "" {
			rows = append(rows, row)
		}
	}

	if len(rows) == 0 {
		sb.WriteString("\nMembers: (none — you are the only member of this squad)\n")
		return sb.String()
	}

	sb.WriteString("\nMembers:\n")
	for _, r := range rows {
		sb.WriteString(r)
	}
	return sb.String()
}

// renderMemberRow renders a single roster row, returning "" if the member
// can't be resolved or should be skipped (e.g. archived agent).
func renderMemberRow(ctx context.Context, q *db.Queries, m db.SquadMember) string {
	id := util.UUIDToString(m.MemberID)
	role := strings.TrimSpace(m.Role)
	switch m.MemberType {
	case "agent":
		ag, err := q.GetAgent(ctx, m.MemberID)
		if err != nil {
			return ""
		}
		if ag.ArchivedAt.Valid {
			return ""
		}
		return formatRosterRow(ag.Name, "agent", role, formatMention(ag.Name, "agent", id))
	case "member":
		user, err := q.GetUser(ctx, m.MemberID)
		if err != nil {
			return ""
		}
		// Mention syntax for humans uses the user_id (matches the rest of
		// the product — see util.MentionRe and frontend mention payloads).
		userID := util.UUIDToString(m.MemberID)
		return formatRosterRow(user.Name, "member (human)", role, formatMention(user.Name, "member", userID))
	default:
		return ""
	}
}

func formatRosterRow(name, kind, role, mention string) string {
	var sb strings.Builder
	sb.WriteString("- ")
	sb.WriteString(name)
	sb.WriteString(" — ")
	sb.WriteString(kind)
	if role != "" {
		sb.WriteString(`, role: "`)
		sb.WriteString(role)
		sb.WriteString(`"`)
	}
	sb.WriteString(" — `")
	sb.WriteString(mention)
	sb.WriteString("`\n")
	return sb.String()
}

// formatMention emits a mention markdown string that round-trips through
// util.ParseMentions. The label is the human display name; the link target
// uses the mention:// scheme with the entity type and UUID.
func formatMention(name, mentionType, id string) string {
	return "[@" + name + "](mention://" + mentionType + "/" + id + ")"
}
