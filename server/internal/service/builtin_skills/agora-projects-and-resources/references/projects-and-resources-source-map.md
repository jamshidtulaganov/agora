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
