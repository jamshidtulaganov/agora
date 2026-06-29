All claims in this MDX are grounded in the three required source files (read in full) plus the skill's own source map:

WIRE FORMAT & PARSER (server/internal/util/mention.go):
- Format `[@Label](mention://<type>/<id>)`, optional @, non-greedy label, id = hex+dashes or `all` → MentionRe at mention.go:16 (verbatim regex copied).
- Five types member|agent|squad|issue|all → regex alternation, mention.go:16.
- ParseMentions dedups on m[2]+":"+m[3], returns Mention{Type:m[2], ID:m[3]} → mention.go:24-37.
- HasMentionAll / IsMentionAll → mention.go:19-21, 40-47.
- DOC COMMENT UNDERCOUNT: struct comment lists only "member","agent","issue","all", omits squad → mention.go:7. Regex includes squad → mention.go:16. Stated as "trust the regex" per task.

PER-TYPE ENQUEUE (server/internal/handler/comment.go):
- computeMentionedAgentCommentTriggers → comment.go:1335.
- squad branch resolves LeaderID, adds mention_squad_leader → comment.go:1352-1391.
- Literal guard `if m.Type != "agent" { continue }` → comment.go:1394-1396 (copied verbatim).
- agent branch adds mention_agent → comment.go:1397-1424.
- enqueueCommentAgentTriggers: mention_squad_leader→EnqueueTaskForSquadLeader (1106-1112), mention_agent→EnqueueTaskForMention (1113-1119).
- Trigger-source enum names → comment.go:797-801.
- member/issue enqueue NOTHING (reach neither branch) → confirmed by source map line 41 + code structure.
- computeCommentAgentTriggers folds in mention triggers → comment.go:1124-1160.

@all SUPPRESSION:
- commentMentionsOthersButNotAssignee returns true on HasMentionAll → comment.go:1206, 1219-1221.
- consulted in computeCommentAgentTriggers assignee path → comment.go:1140-1142.
- @all is neither squad nor agent → skipped by 1394-1396.
- `[@all](mention://all/all)` literal both slots → regex id group accepts `all`, mention.go:16.

UUID LOOKUP (CLI id sources, from skill source map + SKILL.md):
- member → user_id NOT membership id; agora workspace member list --output json (cmd_workspace.go:465).
- agent → id; agora agent list (cmd_agent.go:365).
- squad → id; agora squad list (cmd_squad.go:57).
- formatMention emits mention://member/<user_id> → squad_briefing.go:189-190, 216-218.

SILENT NO-OPS:
- name-where-uuid: parse fail (non-hex) → mention.go:16.
- unknown UUID: parses, GetAgentInWorkspace/GetSquadInWorkspace err → continue → comment.go:1359-1361, 1408-1410.
- already-pending: HasPendingTaskForIssueAndAgent → continue → comment.go:1384-1390, 1417-1423.
- archived/no runtime: RuntimeID invalid or ArchivedAt → comment.go:1376-1378, 1408-1410.
- private agent: canAccessPrivateAgent → comment.go:1380-1382, 1413-1415.

PREVIEW & SUPPRESSION:
- PreviewCommentTriggers reuses computeCommentAgentTriggers → comment.go:832-877.
- Response fields id/name/avatar_url/source/reason → comment.go:783-793.
- CreateCommentRequest.SuppressAgentIDs → comment.go:770-776.
- filterSuppressedCommentAgentTriggers post-filter → comment.go:1066-1087, 1057-1064.
- parseUUIDSliceOrBadRequest boundary parse → comment.go:925-928.

CROSS-LINKS: only linked to existing/allowed siblings — squads-routing.mdx (exists at apps/docs/content/docs/developers/agentic/), comment-trigger-pipeline (allowed sibling slug). Both in the permitted list.

CONVENTIONS: frontmatter title+description one line; imports only Callout + Mermaid; first block Callout title="Audience" stating both; angle-bracket placeholders kept inside backticks/code fences; verbatim Go identifiers inline; ## Source files table + same-PR drift sentence per CLAUDE.md rule on builtin_skills.