---
name: agora-working-on-issues
description: "Use when working on a Agora issue after the runtime has provided the trigger context — to apply the product contracts the runtime brief does not encode: how PR linking differs from close intent, how to read a linked PR's real state via the pull-requests CLI, which metadata keys are high-signal, what status changes trigger on the server, how to stop and ask a human with `agora issue escalate` instead of guessing, and how sub-issue create status (todo vs backlog) controls whether assigned agents start immediately."
user-invocable: false
allowed-tools: Bash(agora *), Bash(git *), Bash(gh *)
---

# Working on Agora issues

Product contracts the runtime brief does not fully encode: PR linking vs close
intent, reading linked-PR state, metadata keys, status side effects, and
sub-issue enqueue behavior.

For building mention links, load `agora-mentioning` instead — not this skill.

Every contract below is traced to source in
`references/working-on-issues-source-map.md`.

## PR linking and close intent are two distinct contracts

The GitHub webhook runs two separate scans over an incoming PR. They are not the
same gate and they read different fields.

**Linking** scans the PR **title, body, OR branch** for a routable issue key
(`PREFIX-NUMBER`, e.g. `MUL-2759`). Each match writes an issue ↔ PR link row.
This is the link that `agora issue pull-requests` reads back.

```text
MUL-2759: add built-in issue working skill        # title prefix → links
agent/matt/mul-2759-working-on-issues             # branch ref   → links
```

**Close intent** is stricter and is a separate scan over **title or body only —
never the branch**. It fires only for a key placed immediately after a closing
keyword (`Closes` / `Fixes` / `Resolves`, optional `:` then whitespace). That
adjacency is what sets the link row's close-intent flag, the gate that
auto-advances the issue to `done` when the PR merges.

```text
Closes MUL-2759                                    # links AND records close intent
Fixes MUL-2759
Resolves MUL-2759
Fix login MUL-2759                                 # links only — keyword not adjacent
```

Consequence: a bare title prefix or a branch reference links the PR but does not
close the issue on merge. A closing keyword immediately adjacent to the issue key
records close intent; on merge, that close intent can move the linked issue to
`done`.

### Default for code-changing issue work

When an issue run changes code in a checked-out GitHub repo, the default handoff
is to open or update a PR before posting the final Agora issue comment, unless
the user explicitly asked for a local-only change or no PR. This is a default, not
an unconditional command: if no code changed, say no PR is needed; if PR creation
is blocked by auth, failing tests, or missing remote state, report that blocker
instead of pretending the run is complete.

Use a routable issue key in the PR title, body, or branch so the webhook can link
the PR back to the issue. If the PR should close the issue on merge, put the key
immediately after a closing keyword in the title or body, for example:

```text
MUL-2759: fix login redirect        # links only
Closes MUL-2759                     # links and records close intent
```

In the final issue comment, include the PR URL when a PR exists. If the task did
not produce a PR because no code changed or the user asked not to create one, say
that explicitly.

## Reading a linked PR's real state

When a step depends on PR state, query Agora's link table — do not infer it
from branch names, GitHub search, memory, or `pr_url` metadata (which can be
stale).

```bash
agora issue pull-requests <issue-id> --output json
```

Returns `{"pull_requests": [...]}`. Each element exposes:

- `number`, `html_url`, `title`
- `state` — the PR lifecycle as a **single enum**, one of `merged`, `closed`,
  `draft`, `open`. There is no separate `draft` or `merged` boolean in the
  response; the server folds them into `state` (merged wins, then closed, then
  draft, else open).
- `merged_at` — non-null once merged; a second confirmation of `state: merged`.
- `mergeable_state` — mirrors GitHub (`clean` / `dirty` surfaced; other values
  round-trip as unknown).
- `checks_conclusion` — aggregated CI: `passed`, `failed`, `pending`, or `null`
  when no check suite has been observed. Backed by `checks_passed`,
  `checks_failed`, `checks_pending` counts.

So "is it merged?" is `state == "merged"` (or `merged_at != null`); "is it still
a draft?" is `state == "draft"`; CI status is `checks_conclusion`.

If the command returns no linked PRs after a PR was opened, the link scanner did
not observe a routable issue key in the PR title/body/branch.

## Metadata: high-signal keys only

Metadata is durable issue state. Reading metadata is safe. Writing a metadata key
is a state mutation and should be tied to an explicit task requirement to record
that state for later readers or runs.

High-signal keys (reuse these names so queries stay consistent):

- `pr_url`
- `pr_number`
- `pipeline_status`
- `deploy_url`
- `external_issue_url`
- `waiting_on`
- `blocked_reason`
- `decision`

Not metadata: logs, summaries, files touched, timestamps, attempt counts,
investigation notes. Those belong in the result comment.

```bash
agora issue metadata set <issue-id> --key pr_url --value <url>
agora issue metadata delete <issue-id> --key <stale-key>
```

`--value` is JSON-parsed by default (bool/number are sniffed); pass `--type
string|number|bool` to force a type.

## Status changes have server side effects

A status change is not cosmetic — the server enqueues or skips agent work based
on it. These are the contracts, not advice:

- **`backlog`** parks an agent-assigned issue: the assignee is set but no task
  fires. Moving `backlog → todo` (or any non-done/non-cancelled status) enqueues
  the assigned agent then.
- **`in_review`** is an accepted issue status. Some workflows use it while a PR
  is open and awaiting review; moving to it is an explicit mutation.
- **`done`** on a child issue posts a system comment on its parent. If a PR
  carries close intent (`Closes MUL-XXXX`), it advances the issue to `done`
  itself on merge — you do not also need to flip it manually.
- **`cancelled`** stops outstanding work; treat it as a user-driven decision.

## Stop and ask instead of guessing

You cannot ask a question mid-run — every Claude invocation carries
`--disallowedTools AskUserQuestion`, because a headless run has no UI for a
prompt to render in. The out-of-band hatch is a CLI verb, and using it is a
state change, not a comment:

```bash
agora issue escalate <issue-id> --need "the one thing you need decided" --tried "what you ruled out"
```

Contracts, not advice:

- **It returns immediately.** It does not poll and does not block your run —
  that is the whole difference from `agora telegram ask`, which blocks the
  process for up to 60 minutes. Post your final comment saying where you
  stopped, then end the run.
- **Your task parks, it does not fail.** The row moves to `waiting_human`; no
  retry fires, no `qa:*` label changes, and the runtime slot is freed.
- **You are resumed with the answer in the SAME session.** The human's reply
  is posted as a comment and becomes your next run's trigger, so you keep
  your context instead of re-reading the repository.
- **One open escalation per issue** — a database guarantee, not a convention.
  A second `escalate` on the same issue REPLACES the first rather than
  queueing a second question for the same person.
- **Only a human can clear it.** Never you, never another agent.

Optional flags: `--option "A" --option "B"` when the answer is a choice
between concrete alternatives, and `--kind blocked|permission|risk` when it is
not a plain question (default `question`).

Escalate when the requirement is genuinely ambiguous, you need an access or
credential decision, or you would be guessing at something expensive to undo.
Do NOT escalate for anything you can find out yourself by reading the repo,
the issue, or the linked PR.

### When a budget stops you

Runs carry per-task budgets (turn cap, and optionally a dollar ceiling and a
wall clock). A run that exhausts one is NOT retried — it raises a
`kind: budget` escalation automatically and waits for a human to raise the
budget or narrow the task. You do not raise that one yourself; you just see
your run end.

## Sub-issues: `todo` starts work now, `backlog` parks it

On an agent-assigned issue, create status decides whether the assignee fires
immediately. A non-backlog status (e.g. `todo`) enqueues the agent at create
time; `backlog` sets the assignee without triggering.

Parallel children — all start now:

```bash
agora issue create --title "..." --parent <issue-id> --assignee <agent> --status todo
```

Strictly serial children — park later steps, promote one at a time:

```bash
agora issue create --title "Step 2: ..." --parent <issue-id> --assignee <agent> --status backlog
agora issue status <child-id> todo   # promote when the previous step is truly done
```

Creating every serial step as `todo` enqueues the whole chain at once.

## Incorrect → correct

PR title (link the issue):

```text
Fix login redirect                  # incorrect — no issue key, won't link
MUL-2759: fix login redirect        # correct — links the PR
```

Serial sub-issues (don't start the whole chain):

```bash
# incorrect — both fire immediately
agora issue create --title "Step 2" --parent <issue-id> --assignee <agent> --status todo
agora issue create --title "Step 3" --parent <issue-id> --assignee <agent> --status todo

# correct — parked, promote in turn
agora issue create --title "Step 2" --parent <issue-id> --assignee <agent> --status backlog
agora issue create --title "Step 3" --parent <issue-id> --assignee <agent> --status backlog
```

## References

`references/working-on-issues-source-map.md` — accurate `file:line` for every
contract above: the `pull-requests` CLI and route, the PR response field list,
the `escalate` verb, its route and its parking / resume path,
`derivePRState`, the two-path link (`extractIdentifiers`) vs close-intent
(`extractClosingIdentifiers`) proof, the backlog enqueue lines, child-done
notify, and the metadata CLI. Re-derive before depending on an exact line.
