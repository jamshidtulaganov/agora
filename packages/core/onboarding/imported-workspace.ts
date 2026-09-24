import type { Workspace } from "../types";

/**
 * True for a workspace an importer created for its members — today the Zoho
 * Projects migration, which stamps `settings.zoho_project_id`. A person who
 * lands in onboarding already belonging to one of these gets the member
 * setup (photo, notifications, a tour) instead of the create-a-workspace
 * flow built for people starting from scratch.
 *
 * `settings` is server-driven JSON, so read it defensively: anything but a
 * non-empty string is "not imported".
 */
export function isImportedWorkspace(workspace: Pick<Workspace, "settings"> | null | undefined): boolean {
  const settings = workspace?.settings;
  if (!settings || typeof settings !== "object") return false;
  const id = (settings as Record<string, unknown>).zoho_project_id;
  return typeof id === "string" && id.trim() !== "";
}
