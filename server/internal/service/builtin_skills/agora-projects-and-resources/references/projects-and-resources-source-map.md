# Projects and resources source map

- `server/cmd/agora/cmd_project.go` registers project `list`, `get`, `create`, `update`, `delete`, and `status`.
- The same file registers `project resource list/add/update/remove`.
- `project create --repo` attaches `github_repo` resources during project creation.
- `project resource add` supports shortcuts for `github_repo` (`--url`, `--default-branch-hint`) and `local_directory` (`--local-path`, `--daemon-id`, `--ref-label`), or generic `--ref '<json>'`.
- `project resource update` merges shortcut edits with existing `resource_ref` so a partial edit does not clobber required fields.
- `server/cmd/server/router.go` exposes `/api/projects` plus `/api/projects/{projectId}/resources` routes.
- `server/pkg/db/queries/project_resource.sql` is the CRUD query surface for `project_resource` rows.
- Project resources are written into `.agora/project/resources.json` for agent workdirs.
- `server/cmd/agora/cmd_project.go` also registers `project qa-manifest get/set/build` (`set` reads `--file` or stdin and PUTs the manifest JSON).
- `server/internal/handler/project_qa_manifest.go` implements `PUT /api/projects/{id}/qa-manifest` (merge-writes ONLY the `qa_manifest` key in `project.settings`; validates base_url + non-empty routes/flows/auth) and `POST /api/projects/{id}/qa-manifest/build` (queues the lead agent's derivation task).
- `maybeEnqueueQAManifestBuild` fires the background build on project create (`server/internal/handler/project.go`) and on the first `github_repo` attach (`server/internal/handler/project_resource.go`), guarded: agent lead + repo present + no existing `qa_manifest`.
- `server/internal/handler/slice_action.go` (`sliceActionQAManifestContext`) injects the manifest — including `known_issues` and `notes` — into every QA slice instruction and every daemon claim (`server/internal/handler/daemon.go`).
- Knowledge base: `knowledge_item` rows (migration `146_knowledge_item`, queries `server/pkg/db/queries/knowledge_item.sql`) are compiled into the `<slug>-kb` skill by `server/internal/service/knowledge_compile.go` (`RecompileKB`, keyed by resolved KB name; splices only the `agora:kb:items` marker region). Agent ` ```knowledge-items``` ` comment blocks are parsed by `server/internal/service/knowledge_item.go` (`CaptureKnowledgeItems`), wired from `server/internal/handler/comment.go` and `createAgentComment` in `server/internal/service/task.go` (pre-expansion content).
- `server/internal/handler/issue.go` (`maybeEnqueueKnowledgeCapture`) fires the capture on a genuine ->done transition; `server/internal/handler/knowledge_synth.go` (`resolveKBSynthesizer`) finds/auto-provisions the "KB Synthesizer" agent and stamps its UUID (the ingest trust anchor). Human review CRUD: `server/internal/handler/knowledge_item.go` (`/api/projects/{id}/knowledge/items`, `/api/knowledge-items/{itemId}`; mutations require a human actor).
