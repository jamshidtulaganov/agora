// Zoho import-browser types. Mirror the backend payloads in
// server/internal/handler/zohoprojects_endpoints.go (ZohoProjectResponse,
// ZohoImportRequest/Response) and zohosprints_endpoints.go
// (ZohoSprintsProjectResponse, ZohoSprintsImportRequest/Response).

/** One Zoho Projects project in the configured portal. */
export interface ZohoProject {
  id: string;
  name: string;
  status: string;
}

/** Import selector for Zoho Projects: explicit ids and/or "all". */
export interface ZohoImportRequest {
  project_ids?: string[];
  all?: boolean;
  /** Restrict to a single Zoho user's tasks (zpuid). Empty = all owners. */
  owner_zpuid?: string;
}

/**
 * Tally of a Zoho import run. Asynchronous like the Bitrix import: the server
 * returns 202 with `accepted` (projects enqueued) once the set is resolved;
 * per-task reconcile runs in the background and issues stream in over the
 * websocket, so created/updated/skipped are 0 in this response.
 */
export interface ZohoImportResponse {
  created: number;
  updated: number;
  skipped: number;
  accepted: number;
  errors: string[];
}

/** One Zoho Sprints project in the configured team. */
export interface ZohoSprintsProject {
  id: string;
  name: string;
}

/** Import selector for Zoho Sprints. */
export interface ZohoSprintsImportRequest {
  project_ids?: string[];
  all?: boolean;
}

/** Tally of a Zoho Sprints import run (asynchronous, like Projects). */
export interface ZohoSprintsImportResponse {
  accepted: number;
  errors: string[];
}
