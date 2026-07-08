# Runtimes and repos source map

- `server/cmd/agora/cmd_runtime.go` registers `runtime list`, `usage`, `activity`, and `update`.
- `runtime list` reads `/api/runtimes` and prints `id`, `name`, `runtime_mode`, `provider`, `status`, and `last_seen_at`.
- `runtime update` posts to `/api/runtimes/{runtime-id}/update`; with `--wait` it polls update status.
- `server/cmd/agora/cmd_repo.go` registers `repo checkout <url> [--ref]`.
- `repo checkout` requires `AGORA_DAEMON_PORT`, sends `workspace_id`, `workdir`, `ref`, `agent_name`, and `task_id` to local daemon `/repo/checkout`, then prints the checked-out path.
- `server/cmd/server/router.go` registers daemon APIs under `/api/daemon`, including workspace repos and task claim.
- `server/internal/daemon/daemon.go` claims tasks, prepares workdirs, launches provider CLIs, and reports completion.
- `server/internal/daemon/execenv/runtime_config.go` injects task/project/repo context into agent workdirs.
- `server/internal/daemon/local_directory.go` resolves a project's `local_directory` resource for this daemon, validates the path (denylist + protected-home-subtree + R/W probe), and serialises tasks on it via `LocalPathLocker`.
- `server/internal/daemon/local_allowlist.go` enforces on-machine owner approval (`checkLocalDirApproved` reads `~/.agora/local-dirs.json` + `AGORA_LOCAL_DIR_ALLOWLIST`); `agora daemon allow-dir` in `server/cmd/agora/cmd_daemon.go` writes it.
- `server/internal/daemon/local_git.go` isolates in-place git runs: dirty-tree snapshot to `refs/agora/backup/<task>`, agent branch off the user's HEAD, teardown-if-no-commits, and the change summary appended to the result comment. Wired through `acquireLocalDirectoryLockIfNeeded` and `handleTask` in `daemon.go`.
