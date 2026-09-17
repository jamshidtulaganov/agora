# Assistant implementation handoff for Claude agents

Updated: 2026-09-17. Reviewed HEAD: `96e5f4d7`, plus concurrent working-tree changes.

## Shared work already completed

- CSP insertion uses a trusted wrapper before supplied HTML. Do not restore regex scanning or parse untrusted markup before inserting the policy.
- Confirmation claim and execution intent commit together. Receipt persistence has a fresh bounded context after execution, independent of request cancellation.
- Migration 200 retains confirmation records when their workspace is deleted.
- Confirmation cards read authoritative operation status and outcome. Unknown outcomes block repeat confirmation and offer Check status.
- Completed handler validation refusals preserve actionable errors; interrupted or ambiguous effects remain uncertain.

These changes span backend, core, views, and four locales. Preserve them when integrating your current work.

## Verification evidence

Before the latest artifact-history and conversation-UI changes:

- Full typechecks passed.
- 3,119 frontend tests passed.
- Final standalone handler suite passed: `/tmp/agora-sol-handler-final.log`.
- Browser checks verified that comment, script-text, and attribute head decoys cannot hide CSP. Inline scripts ran; fetch and external image requests were blocked.
- Full `make check` stopped in Go tests. The assistant last-owner refusal regression was then fixed and the handler suite rerun successfully. The remaining environment-dependent Bitrix failure is described below. Full browser E2E was not reached.

Latest review typecheck passed: all 7 tasks succeeded. Log: `/tmp/agora-claude-review-typecheck.log`. The working tree is changing, so this does not verify subsequent edits.

## Concrete verification blocker: Bitrix test environment

`configureBitrixEnv` in `server/internal/handler/bitrix_sync_test.go` resets several integration environment variables but does not reset `BITRIX_PUSH_SYSTEM_COMMENTS`.

`make check` sources `.env`. When that flag is false, `TestMirrorIssueStatusToBitrix` fails with `expected a courtesy comment to Bitrix`. The standalone handler suite passed without that loaded app configuration.

Recommended test-only fix: explicitly set `BITRIX_PUSH_SYSTEM_COMMENTS` to `true` in the fixture that expects courtesy comments. Tests for disabled behavior should override it locally. Preserve the production setting and production behavior.

Verify with the flag initially false, then rerun `make check`. Do not run multiple handler test binaries concurrently: shared TestMain setup/cleanup can collide.

## Current integration work to verify

### Confirmed artifact-flow failure from the owner's screenshot

Conversation `a127704f-ade9-4aa8-8566-5d4688b2ef2c`:

- User requested an artifact at 02:04:51 local time on 2026-09-17.
- `create_artifact` successfully saved “Projects across workspaces”, version 1, at 02:04:56.089; its tool result persisted immediately afterward.
- The following model completion failed at 02:04:56.371 with HTTP 429 `rate_limit_exceeded`.
- Another user request at 02:04:58 also failed with 429. The database still contains the one successfully created artifact.

The current generic “could not reach its model” failure hides a successful artifact creation followed by failed narration. Preserve the artifact/result and show partial completion with its link. Retry only the failed model continuation with bounded backoff; do not rerun artifact creation. Distinguish rate limits from connectivity failures. Evidence: local database timestamps and the matching session entries in Claude's scratchpad `logs/backend.log`.

- Migration 201 introduces artifact revision storage. It was added after the passing handler run; apply it and verify artifact creation/update/history and ownership tests.
- Conversation UI changes include auto-title, follow-up suggestions, panel state, scrolling, and transcript dates. Rerun affected frontend tests after edits settle.
- Keep API/schema/type changes together, including unknown outcome handling.
- The scenario harness has changed since the previous recorded run. Re-run the intended snapshot; do not carry old success counts forward.

## Remaining HTML limitation

The CSP fix prevents policy suppression and restricts governed resource requests. It does not establish complete network isolation: same-frame navigation remains a separate concern. Avoid describing HTML artifacts as fully unable to send network requests until that behavior has its own verified containment.

## Coordination

This file is a local handoff, not evidence that another agent has read it. Avoid overlapping edits to files currently owned by another session. Run database verification serially and record the tested revision or snapshot.

## Sol integration update — 2026-09-17

- Added shared Artifacts library and web/desktop sidebar routes. Owner-scoped API lists 40 summaries per page; library supports loaded-page search, kind filters, preview, and opening the original chat. Layout uses the full available width and aligns left, per the owner's latest request.
- Added Preview/Code and downloads to the artifact pane. Concurrent Claude work is extending revision/workbench support; preserve both sets of changes when integrating.
- Added project selection and readable text/code file attachments to the shared Assistant composer. Up to five files, 64 KiB each, 128 KiB aggregate. Selection is captured with the send/retry context. Project resources are metadata inventory, not automatically fetched repository content.
- Backend checks workspace/project/file access at acceptance and run start. Repository URL credentials/query/fragment are removed from prompt context.
- Fixed older artifact chats being replaced by recent-session healing. Missing from a capped list is not proof of deletion; transient session-detail failures preserve selection.
- Malformed artifact library pages now produce a retryable error instead of silently stopping pagination.
- Applied migrations through 202. Focused context/library permission and file-limit tests pass (five tests). Frontend agent checks covered composer/send/retry controls, safe preview/source display, and older session navigation.
- Applied the Bitrix fixture-only environment fix described above. Full pipeline verification is still in progress; do not report it as passed based only on focused checks.

### Final verification outcome

- `make check`: TypeScript typecheck, all frontend unit tests, migrations, and all Go packages passed. Browser stage: 18 passed, 3 Bitrix fixture tests skipped, 2 navigation timeouts while the development server was compiling.
- The first targeted navigation retry found the shared frontend had stopped (`ERR_CONNECTION_REFUSED`). Restarted the local frontend; the three navigation tests then passed in 31.5 seconds. No navigation production code change was needed.
- Manually verified the Artifacts sidebar entry, left-aligned full-width library, and Assistant Project/Attach file controls in the browser. Screenshot: `/tmp/agora-artifacts-layout.png`.
- Logs: `/tmp/agora-artifacts-full-check.log`, `/tmp/agora-artifacts-navigation-recheck.log`, `/tmp/agora-artifacts-handler-focused.log`.
