---
name: agora-runtimes-and-repos
description: "Use when inspecting or debugging Agora runtimes, daemon task claiming, agent not running, workdir/session reuse, or repository checkout. Covers runtime online/offline state, daemon heartbeat/claim chain, task-scoped repo checkout, project repo context, local_directory caveats, and safe diagnostic commands."
user-invocable: false
allowed-tools: Bash(agora *)
---

# Agora Runtimes and Repos

## Quick start

For "agent did not run" or "repo checkout failed", read the chain before changing anything:

```bash
agora agent get <agent-id> --output json
agora runtime list --output json
agora repo checkout <repo-url>
```

Runtime and repo commands affect active agent execution. Do not restart daemons, update runtimes, or check out arbitrary repos just to test.

## Core model

A runtime is the execution target behind an agent. A daemon owns local runtime processes and claims queued tasks from the server.

The chain is:

1. user action creates or updates an `agent_task_queue` row;
2. the task points at an agent and runtime;
3. server wakes the runtime over daemon websocket when possible;
4. daemon polls/claims the task;
5. server returns task context, repos, project resources, prior session/workdir hints, and task token;
6. daemon prepares a workdir and launches the provider CLI;
7. `agora repo checkout` talks to the local daemon, not directly to GitHub.

## CLI

```bash
agora runtime list --output json
agora runtime usage <runtime-id> --output json
agora runtime activity <runtime-id> --output json
agora runtime update <runtime-id> --target-version <version> --output json
agora repo checkout <url>
agora repo checkout <url> --ref <branch-or-sha>
```

`runtime update` is a write. `repo checkout` creates a git worktree in the task working directory.

`repo checkout` requires `AGORA_DAEMON_PORT`; it is intended to run inside a daemon task. If absent, you are not in the normal agent checkout path.

## Debugging an agent that did not run

Check in this order:

1. Was a task supposed to be created? Inspect issue/comment/autopilot context.
2. Is the assignee an agent or squad? A squad routes to its leader.
3. Is the agent archived or bound to a runtime the actor cannot use?
4. Is the runtime online? `agora runtime list --output json`.
5. Did the daemon heartbeat recently? Runtime `last_seen_at` is the visible clue.
6. Did the task get claimed or is it stuck pending/running/waiting for local directory?
7. If repo checkout failed, classify it after checking whether repo context was
   present in the task/project context.

## Repos

The runtime brief lists repos available to this task. Treat that list as the authority for agent checkout unless the user explicitly asks to bind a new project resource.

Workspace repos and project resources are not the same thing:

- workspace repo metadata can appear in workspace context;
- `github_repo` project resources are durable project context and can affect future tasks;
- `local_directory` resources point at a path owned by a daemon and carry local-machine assumptions. They are **human-only to mutate**: create/update/delete with an agent task token or cloud PAT returns 403 (do not retry — ask the user to attach the directory themselves). The ref's `daemon_id` must also resolve to a daemon runtime the human caller may use (runtime owner, workspace owner/admin, or `visibility=public`); an unregistered daemon id is a 400.
- Beyond the resource row, the daemon enforces **on-machine owner approval**: a local_directory task fails with `local_directory_error` when the path is not in the machine's allowlist (`~/.agora/local-dirs.json`, or `AGORA_LOCAL_DIR_ALLOWLIST` for headless daemons). The fail message names the fix — the machine owner runs `agora daemon allow-dir <path>` on the daemon host (the desktop folder picker records the approval automatically). Do not retry the task before the owner approves; nothing is broken, consent is just missing.
- When a local_directory is a git repo, the daemon isolates the run: it snapshots any dirty tracked state to `refs/agora/backup/<short-task>` (`git stash create`, zero-touch), then runs the agent on a throwaway `agent/<name>/<short-task>` branch cut from the user's HEAD. If the agent makes **no commits** (a QA gate, an inspection) the branch is torn down and the user's original branch is restored — read-only runs leave no trace, so there is no separate "QA mode" flag. If the agent **did** commit, the working tree stays on the agent branch and the result comment names the branch + the `git checkout` to return. The user's own branch ref is never moved; untracked files are not in the snapshot.

Do not add a project resource just because `repo checkout` failed. First determine whether the user asked for durable project context or just a task checkout.

More source-backed details: `references/runtimes-and-repos-source-map.md`.
