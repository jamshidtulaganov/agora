// Bitrix import-browser types. Mirror the backend payloads in
// server/internal/handler/bitrix_endpoints.go (BitrixGroupResponse,
// BitrixTaskResponse, BitrixImportRequest/Response).

/** One Bitrix workgroup, annotated with the Agora workspace slug it routes to. */
export interface BitrixGroup {
  id: string;
  name: string;
  /** Empty when no route resolves (its tasks would be skipped on import). */
  workspace_slug: string;
}

/** One Bitrix task in a group, with resolved status + already-synced state. */
export interface BitrixTask {
  id: string;
  title: string;
  status: string;
  mapped_status: string;
  tags: string[];
  workspace_slug: string;
  already_synced: boolean;
}

/** Import selector: every task in the given groups and/or the explicit task ids. */
export interface BitrixImportRequest {
  group_ids?: string[];
  task_ids?: string[];
}

/** Tally of an import run. */
export interface BitrixImportResponse {
  created: number;
  updated: number;
  skipped: number;
  errors: string[];
}
