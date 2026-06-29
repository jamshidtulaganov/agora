## loop-prevention — claim → source map

Every load-bearing claim in the page, traced to the exact code read.

### Guard 1 — leader/worker role disambiguation
- `lastTaskWasLeader` returns flag from `GetLatestTaskIsLeaderForIssueAndAgent`, returns `false` on error → squad.go:915-924.
- Doc-comment contract "no prior task → undetermined → treat as non-leader so external trigger can reach leader" → squad.go:908-914 (verbatim paraphrase).
- Assigned-squad call site: `authorType == "agent" && authorID == uuidToString(squad.LeaderID) && h.lastTaskWasLeader(...)` returns `false` → comment.go:1173-1176.
- @squad-mention call site: same condition with `continue` → comment.go:1367-1370, including the comment "An agent that holds both the leader and a worker role ... must still wake its leader role" → comment.go:1363-1366.

### Guard 2 — HasPendingTaskForIssueAndAgent dedup
- Keyed on IssueID + AgentID; `if err != nil || hasPending { return/continue }` (fail closed) → comment.go:1190-1196 (squad leader assignee), comment.go:1384-1390 (@squad mention), comment.go:1417-1423 (@agent mention), squad.go:999-1005 (enqueueSquadLeaderTask), issue_child_done.go:281-287 (triggerChildDoneAgent), issue_child_done.go:336-342 (triggerChildDoneSquad).
- "dedupes rapid-fire enqueues for the same parent (e.g. two children finishing back-to-back)" → issue_child_done.go:242-243 (doc comment).

### Guard 3 — @all / other-mention assignee suppression
- Pre-conditions on assignee trigger: `!h.commentMentionsOthersButNotAssignee(...) && !h.isReplyToMemberThread(...)`, member-only → comment.go:1140-1149.
- `commentMentionsOthersButNotAssignee`: filters out issue mentions, `@all` via `util.HasMentionAll` returns true (suppress), no assignee → suppress, assignee mentioned → allow → comment.go:1206-1233.
- "@all is a broadcast ... suppress agent trigger" → comment.go:1219-1222.
- Squad assignee variant: member + `commentMentionsAnyone(content)` → suppress → comment.go:1177-1179; `commentMentionsAnyone` matches agent/member/squad/all, ignores issue → squad.go:931-939.
- `isReplyToMemberThread`: parent member-started, suppress unless reply/parent mentions assignee (`util.ParseMentions`) or `HasAgentRepliedInThread` → comment.go:1245-1284.
- `/note` short-circuit: `isNoteComment` first token `/note` case-insensitive → comment.go:1042-1060, 1125-1127.

### Guard 4 — child-done same-squad / shared-leader + deliberate agent-path absence
- `notifyParentOfChildDone` inserts system comment via db.Queries, author_type='system', listener short-circuit → issue_child_done.go:51-126; system author_type bypasses listeners → issue_child_done.go:36-46, 118-125.
- `dispatchParentAssigneeTrigger` splits agent vs squad → issue_child_done.go:246-257.
- `triggerChildDoneSquad`: `childAssigneeIsSquad(child, parent.AssigneeID)` return, then `effectiveChildAgentOwner == squad.LeaderID` return → issue_child_done.go:304-351.
- `childAssigneeIsSquad`: child assignee type squad && equals squadID → issue_child_done.go:387-392.
- `effectiveChildAgentOwner`: agent→self, squad→leader, else invalid UUID → issue_child_done.go:353-385.
- Agent path has NO self-guard, MUL-2808, "serial sub-task handoff between two DIFFERENT issues", "not a loop and must fire — see isAgentRunningOnIssue", consistent with computeMentionedAgentCommentTriggers self-mention; bounded by HasPendingTaskForIssueAndAgent → issue_child_done.go:259-295 (doc comment + body).
- @squad child-done wakes leader ONLY, no member fan-out → issue_child_done.go:213-218.
- Self-mention allowance + "runaway loops prevented by HasPendingTaskForIssueAndAgent dedupe" → comment.go:1328-1333.

### Guard 5 — runtime prompt rules
- `buildCommentPrompt` reached when `task.TriggerCommentID != ""` → prompt.go:21-23, 139.
- Agent-author silence rule, "pure acknowledgment ... do NOT reply ... do NOT post 'No reply needed' ... Silence is the preferred way ... do not @mention ... starts a loop" → prompt.go:154-156 (verbatim).
- Squad-leader no_action reminder gated on `strings.Contains(task.Agent.Instructions, "## Squad Operating Protocol")`; call `agora squad activity <issue> no_action --reason` and EXIT, DO NOT post a comment → prompt.go:157-159 (verbatim).
- `RecordSquadLeaderEvaluation` writes `squad_leader_evaluated` activity, outcome action|no_action|failed → squad.go:806-904.
- CreateComment 409 on prior no_action: `HasSquadLeaderNoActionEvaluationForTask` → "squad leader recorded no_action; comments are not allowed for this task" → comment.go:957-967.

### Silent no-op cases
- All five bullets derive directly from Guards 1-5 above; the 409-on-retry case is comment.go:964-966; no_action exit is prompt.go:157-159.

### Conventions compliance
- Frontmatter title+description (one line); imports limited to Callout + Mermaid; first block is `<Callout title="Audience">` stating "Both"; Mermaid flowchart for the layered guard flow; all Go identifiers/paths/enums in inline code or fences; no raw angle brackets in prose (`<issue>`, `<id>` etc. are inside backticks/fences); cross-links only to existing siblings: squads-routing, comment-trigger-pipeline, mention-contract, no-action-protocol, prompt-layers; ends with `## Source files` table + same-PR drift sentence.
