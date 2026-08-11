# Bitrix personal mirror — session changes

Summary of the Bitrix / issue-type work landed on `master` (Aug 2026).

## Goal

Personal realtime mirror of Bitrix tasks for one human, with Bitrix as source of truth; human comments/files may sync outbound; agent/system stay in Agora. Classify work with fundamental `type:*` labels (AI triage), not noisy Bitrix tags or parallel `mode:*` labels.

## Commits (newest last)

| SHA | Summary |
| --- | --- |
| `32ee4acb` | Scope Sales Doctor sync to personal Jamshid mirror; disable status push |
| `202155c2` | Fix allowlist to Bitrix portal email `j.tulaganov@salesdoc.io` |
| `1aaaf344` | Outbound gate uses Agora login `jamshid.swe@gmail.com` |
| `61800efb` | Skip closed Bitrix tasks; delete mirrors when closed; `POST /api/bitrix/cleanup-done` |
| `446bd42c` | Mirror non-type Bitrix tags onto Agora labels |
| `b313f633` | AI triage owns `type:bug\|feature\|question` (ignore noisy bug/feature tags) |
| `78b214f4` | (reverted) briefly added `mode:debugging\|planning` labels |
| `76adbaaa` | Drop `mode:*`; keep `type:*` as the fundamental classifier |

## Behavior now

### Sync scope (Render / `render.yaml`)

- **Inbound allowlist:** `BITRIX_SYNC_USER_EMAILS=j.tulaganov@salesdoc.io` (Bitrix `EMAIL`, one “a”)
- **Outbound actor:** `BITRIX_OUTBOUND_USER_EMAIL=jamshid.swe@gmail.com` (Agora Gmail login)
- **Bitrix SoT:** `BITRIX_PUSH_STATUS=false`
- **Human outbound only:** `BITRIX_PUSH_HUMAN_COMMENTS=true`, `BITRIX_PUSH_SYSTEM_COMMENTS=false`
- Target project: `BITRIX_TARGET_PROJECT=Bitrix` under workspace `sales-doctor`

### Closed tasks

- STATUS `5`/`7` or mapped `done`/`cancelled` → **do not create**
- If already mirrored and then closed → **delete** Agora issue
- Operator cleanup: `POST /api/bitrix/cleanup-done` (or SQL on `sales-doctor` for historical done Bitrix issues)
- Prod DB external access needs your IP on Render Postgres allowlist

### Tags → labels

- Non-type Bitrix tags → Agora labels (aliases collapsed, e.g. DevOps → `server`)
- `bug` / `feature` (and RU/UZ synonyms) are **not** copied as labels
- Intake triage agent (`workspace.settings.triage_agent_id`) sets `type:bug` / `type:feature` / `type:question` from title, description, comments, attachments — **not** from messy multilingual tags

### Agent work style (fundamental `type:*`)

| Label | `draft_code` behavior |
| --- | --- |
| `type:bug` | Debugging: reproduce → root-cause → smallest fix → verify |
| `type:feature` / `type:question` | Planning: acceptance → variants/plan → then build |
| *(no `mode:*` labels)* | Explicitly rejected as a parallel classifier |

### Enable triage

Set on the Sales Doctor workspace:

```json
{ "triage_agent_id": "<agent-uuid>" }
```

Agent needs a runtime and Agora CLI access to attach labels.

## Ops checklist

1. Render env matches the emails / push flags above (Blueprint may not overwrite existing env — edit Dashboard if needed).
2. Deploy backend after these commits.
3. Purge historical done Bitrix issues (`cleanup-done` or SQL) **after** deploy so the old importer cannot re-pull them.
4. Confirm `triage_agent_id` if AI type classification is desired.

## Video → stills for planning (follow-up)

- Bitrix import now **kicks async ffmpeg** as soon as a video attachment is stored (not only on agent assign).
- Claim / `draft_code` inject a dedicated **VIDEO FRAMES FOR PLANNING** block from `*_frame_NNN.jpg` attachments so long truncated descriptions cannot drop the stills.
- Manual retry remains: `POST /api/issues/{id}/video-frames` (requires `ffmpeg` on the backend host).

## Intentionally not done in this session

- Dedicated vision “analyze video” agent skill beyond stills in the brief
- Per-user project map beyond `BITRIX_TARGET_PROJECT`
- UI mode picker (not needed once `type:*` is the classifier)
- Frontend “Extract frames” button (API exists)
