import type { SprintStatus } from "../types";

export const SPRINT_STATUS_ORDER: SprintStatus[] = [
  "planned",
  "active",
  "completed",
];

// Mirrors PROJECT_STATUS_CONFIG. `.label` is the non-translated fallback for
// callers outside the view layer (the views use i18n label maps); `dotColor`
// drives the status dot in the sprints list / picker.
export const SPRINT_STATUS_CONFIG: Record<
  SprintStatus,
  { label: string; dotColor: string; badgeBg: string; badgeText: string }
> = {
  planned: { label: "Planned", dotColor: "bg-muted-foreground", badgeBg: "bg-muted", badgeText: "text-muted-foreground" },
  active: { label: "Active", dotColor: "bg-warning", badgeBg: "bg-warning", badgeText: "text-white" },
  completed: { label: "Completed", dotColor: "bg-info", badgeBg: "bg-info", badgeText: "text-white" },
};
