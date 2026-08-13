// Bitrix import-browser types. Mirror the backend payloads in
// server/internal/handler/bitrix_endpoints.go (BitrixGroupResponse,
// BitrixTaskResponse, BitrixImportRequest/Response).

import { z } from "zod";

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

/** One Bitrix portal user, for the "import by responsible" picker. */
export interface BitrixUser {
  id: string;
  name: string;
  email: string;
  position: string;
}

/**
 * Import selector: every task in the given groups, owned by the given users,
 * and/or the explicit task ids.
 */
export interface BitrixImportRequest {
  group_ids?: string[];
  task_ids?: string[];
  user_ids?: string[];
}

/** Live progress of the most recent (background) import run. */
export interface BitrixImportProgress {
  total: number;
  synced: number;
  running: boolean;
  /** Stopped by an operator rather than finishing. Optional: an older backend
   * omits it, and absent must not read as "cancelled". */
  cancelled?: boolean;
  items: BitrixImportProgressItem[];
}

/** Result of asking the backend to stop the in-flight import. `cancelled` is
 * false when there was no live run to stop. */
export interface BitrixImportCancelResponse {
  cancelled: boolean;
  synced: number;
  total: number;
}

/** Progress for one selected Bitrix user or workgroup inside an import run. */
export interface BitrixImportProgressItem {
  kind: "user" | "group";
  id: string;
  total: number;
  synced: number;
  running: boolean;
}

/** Tally of an import run. */
export interface BitrixImportResponse {
  created: number;
  updated: number;
  skipped: number;
  /**
   * Number of tasks accepted for the asynchronous import. The server returns
   * 202 immediately after resolving the task set; per-task sync runs in the
   * background and issues stream onto the board over the websocket, so
   * created/updated/skipped are 0 in this response.
   */
  accepted?: number;
  errors: string[];
}

/** Runtime-safe wire schema for both selected and self-scoped imports. */
export const BitrixImportResponseSchema = z
  .object({
    created: z.number().default(0),
    updated: z.number().default(0),
    skipped: z.number().default(0),
    accepted: z.number().optional(),
    errors: z.array(z.string()).nullish().transform((value) => value ?? []),
  })
  .loose();

export const EMPTY_BITRIX_IMPORT_RESPONSE: BitrixImportResponse = {
  created: 0,
  updated: 0,
  skipped: 0,
  accepted: 0,
  errors: [],
};

/** Result of an on-demand per-project Bitrix re-sync. The sync is asynchronous
 * (202 + background); `accepted` is how many tasks were enqueued and
 * `bitrix_synced_at` is the RFC3339 timestamp stamped on the project. */
export interface BitrixSyncResult {
  accepted: number;
  bitrix_synced_at: string;
}
