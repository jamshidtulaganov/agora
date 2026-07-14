// Settings → Labs workspace flags (GET/PUT /api/workspace-labs) — experimental
// QA-environment routing toggles.
export interface WorkspaceLabs {
  qa_dev_boxes: boolean;
  qa_fallback_box_id: string;
  // QA tasks execute on the developer's own daemon when it declares a local
  // app for the issue's project (daemon-per-dev). Strict = wait for that
  // daemon instead of falling back when it goes offline.
  qa_dev_runtimes: boolean;
  qa_dev_runtimes_strict: boolean;
}
