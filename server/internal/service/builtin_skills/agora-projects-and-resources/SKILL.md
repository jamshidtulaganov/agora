---
name: agora-projects-and-resources
description: "Use when creating, inspecting, updating, or debugging Agora projects and project resources. Covers durable project context, github_repo and local_directory resources, how resources affect future agent task context, when to bind repos, and when not to mutate resources."
user-invocable: false
allowed-tools: Bash(agora *)
---

# Agora Projects and Resources

## Quick start

Projects are durable context containers. Resources attached to a project can affect future agent tasks.

```bash
agora project list --output json
agora project get <project-id> --output json
agora project resource list <project-id> --output json
```

Project resources are mutated through project resource commands/endpoints. Issue
comments do not create durable project resources.

## Core model

A project groups work and carries durable resources. A resource is not just display metadata; it is context later injected into task briefs and `.agora/project/resources.json`.

Common resource types:

- `github_repo` — durable GitHub repo context, with `resource_ref.url` and optional `default_branch_hint`;
- `local_directory` — daemon-local path context, with `resource_ref.local_path`, `daemon_id`, and optional label.

## CLI

```bash
agora project list --output json
agora project get <project-id> --output json
agora project create --title "<title>" --repo <github-url> --output json
agora project update <project-id> --title "<title>" --output json
agora project status <project-id> in_progress --output json
agora project resource list <project-id> --output json
agora project resource add <project-id> --type github_repo --url <github-url> --output json
agora project resource update <project-id> <resource-id> --url <new-github-url> --output json
agora project resource remove <project-id> <resource-id> --output json
agora project qa-manifest get <project-id>
agora project qa-manifest set <project-id> --file manifest.json
agora project qa-manifest build <project-id>
```

Use `--ref '<json>'` only for resource types or payloads not covered by shortcuts.

`local_directory` resources are **human-only**: any create, update, or delete of
a `local_directory` resource attempted with an agent task token or cloud PAT
returns 403 — including bundling one into project create. Do not retry; ask the
user to attach the directory themselves (Desktop folder picker, or
`agora project resource add <project-id> --type local_directory --local-path <abs-path> --daemon-id <daemon-id>`
from their own CLI session). The ref's `daemon_id` must also resolve to a
daemon runtime registered in the workspace that the human caller may use
(runtime owner, workspace owner/admin, or `visibility=public`); unknown daemon
ids are rejected with 400.

## When to add a resource

Add/update a project resource when the user asks for durable project context: "把这个 GitHub repo 绑到项目上", "以后都用这个 repo", "agent 总是拿不到这个项目的仓库", or "这个项目要在我的本地目录里跑" — for the last one, `local_directory` is human-only, so guide the user through attaching it instead of calling the API yourself.

Project resources are durable and affect future tasks. `agora repo checkout`
is task-local checkout state.

## Debugging wrong context

1. `agora project get <project-id> --output json`.
2. `agora project resource list <project-id> --output json`.
3. Check `github_repo.resource_ref.url`, `default_branch_hint`, and `local_directory.resource_ref.daemon_id`.
4. Updating resources is a durable mutation. After an update, listing the
   resource is the verification path.
5. If resources match the expected task context, inspect runtime/repo checkout
   path next.

## QA manifest

`project.settings.qa_manifest` is the app's KNOWN navigation map for QA agents
(`base_url`, `auth` login recipe, verified `routes`, golden-path `flows`,
`known_issues`, `notes`). It is injected into every QA run and daemon claim on
the project, so agents navigate by map instead of exploring.

- Built automatically in the background by the project's lead agent when a
  project is created with a repo or when the FIRST `github_repo` resource is
  attached (skipped if a manifest already exists — a curated manifest is never
  clobbered).
- `qa-manifest build` re-queues the derivation on demand (even when a manifest
  exists). `qa-manifest set` persists a manifest JSON — this is the required
  final step of a manifest-build task; writing a file in the worktree does NOT
  persist anything.
- Only include routes you saw in code or verified live; dead or role-gated
  paths belong in `known_issues`, never in `routes`.

## Project knowledge base (knowledge-items)

Each project has a `<slug>-kb` skill that is auto-injected into every task on
the project. Its content is **server-compiled** from structured knowledge
items — you do NOT hand-edit it.

- To record a durable learning, post a comment containing a fenced
  ` ```knowledge-items``` ` block with a JSON array. Each item:
  `{"kind": "...", "module": "...", "title": "...", "body": "..."}`.
  `kind` is one of `architecture | gotcha | convention | nav | decision`;
  `title` ≤160 chars states a fact (not a task); `body` ≤1200 chars of plain
  markdown with no code fences or HTML comments; `module` is the affected
  module label or `""`. At most 10 items per comment.
- The server parses the block, deduplicates against existing items (an exact
  restatement just confirms an item), and compiles the active items into the
  `<slug>-kb` skill. Items of instruction-bearing kinds
  (`architecture`/`convention`/`decision`), and anything from a non-synthesizer
  agent, land as `proposed` and wait for human approval before they compile in.
- **Never run `agora skill` to edit a `<slug>-kb` skill, and never touch the
  content between the `<!-- agora:kb:items:begin -->` /
  `<!-- agora:kb:items:end -->` markers** — that region is machine-managed and
  a whole-content write that drops it is treated as an error. Emit
  knowledge-items instead.
- Capture runs automatically when an issue transitions to `done` (a dedicated
  "KB Synthesizer" agent is triggered); you can also emit a block from any
  task that learned something durable.

## Side effects

Project create/update/delete/status, project resource add/update/remove, and `qa-manifest set` mutate durable workspace state and affect future tasks. Creating a project with a repo (or attaching the first repo) also queues background knowledge-base and QA-manifest builds for an agent lead. `local_directory` resources cannot be changed by agents at all — the API returns 403 for machine credentials on create/update/delete; a 403 there is the expected contract, not an error to work around.

More source-backed details: `references/projects-and-resources-source-map.md`.
