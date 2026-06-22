"use client";

import type { SprintStatus } from "@agora/core/types";
import { useT } from "../../i18n";

// i18n-aware label map for sprint status, mirroring `useProjectStatusLabels`.
// Lives in the projects view because sprints are surfaced inside the project
// detail; reads from the `projects` namespace `sprints` subtree.
export function useSprintStatusLabels(): Record<SprintStatus, string> {
  const { t } = useT("projects");
  return {
    planned: t(($) => $.sprints.status_planned),
    active: t(($) => $.sprints.status_active),
    completed: t(($) => $.sprints.status_completed),
  };
}
