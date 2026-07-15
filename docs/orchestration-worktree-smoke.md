# Local orchestration worktree smoke

This smoke proves the Git boundary used by a local-directory orchestration run.
It uses a real temporary repository and real `git worktree` commands; it does
not mock branch creation, merge ancestry, or detached verification checkouts.

## Automated contract

Run:

```bash
cd server
go test ./internal/daemon -run TestLocalOrchestrationSmoke_ParallelBranchesIntegrateAndVerifyExactHead
```

The test verifies this complete handoff:

```text
immutable base
  ├─ backend worker branch ─┐
  └─ frontend worker branch ├─ integration HEAD
                            ├─ detached QA worktree
                            └─ detached Review worktree
```

Assertions:

- both worker worktrees start at the same pinned base commit;
- the workers create distinct branches and committed HEADs;
- the integration HEAD contains both dependency commits by ancestry;
- QA and Review can coexist as separate, clean, detached worktrees;
- both verification worktrees open the exact integration HEAD and contain both
  handoffs;
- the developer's source checkout stays on its original branch.

Related focused regression suites:

```bash
cd server
go test ./internal/daemon ./internal/handler
```

These additionally cover short-lived source-path locking, compare-and-set base
pinning, same-agent serialization, independent-agent fan-out, capability-aware
DAG validation, integration evidence, and read-only completion rejection when
HEAD moves or a verification worktree becomes dirty.
