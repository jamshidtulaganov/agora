import type { Issue, IssueStatus, IssuePriority } from "@agora/core/types";
import type { ActorFilterValue } from "@agora/core/issues/stores/view-store";

export interface IssueFilters {
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  assigneeFilters: ActorFilterValue[];
  includeNoAssignee: boolean;
  creatorFilters: ActorFilterValue[];
  projectFilters: string[];
  includeNoProject: boolean;
  labelFilters: string[];
  sprintFilters: string[];
  /** Bitrix workgroup ids ("projects" in Bitrix's UI) read from issue
   * metadata. Kept separate from projectFilters because Bitrix routing collapses
   * every workgroup into one Agora project, and only sprint-named groups get a
   * sprint — so neither existing dimension can answer "which Bitrix project did
   * this come from". OR semantics, mirroring labels. */
  bitrixGroupFilters?: string[];
  // When `agentRunningFilter` is true, only keep issues whose id is in
  // `runningIssueIds`. The set is derived by the caller from
  // `agentTaskSnapshot` (one pass over running tasks) so filter.ts stays
  // free of any data-fetching dependency.
  agentRunningFilter?: boolean;
  runningIssueIds?: ReadonlySet<string>;
}

/**
 * Filter issues using positive selection model.
 * Empty arrays = no filter (show all). Non-empty = show only matching.
 *
 * Assignee has a special "No assignee" toggle (includeNoAssignee):
 * - When only includeNoAssignee is true → show only unassigned issues
 * - When assigneeFilters has items → show only those assignees' issues
 * - When both → show matching assignees + unassigned
 */
export function filterIssues(issues: Issue[], filters: IssueFilters): Issue[] {
  const { statusFilters, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, projectFilters, includeNoProject, labelFilters, sprintFilters, bitrixGroupFilters, agentRunningFilter, runningIssueIds } = filters;
  const hasAssigneeFilter = assigneeFilters.length > 0 || includeNoAssignee;
  const hasProjectFilter = projectFilters.length > 0 || includeNoProject;
  // Empty set passed without `agentRunningFilter` is a no-op. When the
  // filter is on but the set is missing/empty, hide everything — the
  // user opted into "only running" and there is nothing running.
  const applyAgentRunning = agentRunningFilter === true;

  return issues.filter((issue) => {
    if (applyAgentRunning && !(runningIssueIds?.has(issue.id) ?? false))
      return false;

    if (statusFilters.length > 0 && !statusFilters.includes(issue.status))
      return false;

    if (priorityFilters.length > 0 && !priorityFilters.includes(issue.priority))
      return false;

    if (hasAssigneeFilter) {
      if (!issue.assignee_id) {
        // Unassigned issue — show only if "No assignee" is checked
        if (!includeNoAssignee) return false;
      } else if (assigneeFilters.length > 0) {
        // Assigned issue — show only if assignee is in the filter list
        if (!assigneeFilters.some(
          (f) => f.type === issue.assignee_type && f.id === issue.assignee_id,
        )) return false;
      } else {
        // Only "No assignee" is checked, no specific assignees → hide assigned issues
        return false;
      }
    }

    if (
      creatorFilters.length > 0 &&
      !creatorFilters.some(
        (f) => f.type === issue.creator_type && f.id === issue.creator_id,
      )
    ) {
      return false;
    }

    if (hasProjectFilter) {
      if (!issue.project_id) {
        if (!includeNoProject) return false;
      } else if (projectFilters.length > 0) {
        if (!projectFilters.includes(issue.project_id)) return false;
      } else {
        // Only "No project" is checked → hide issues that have a project
        return false;
      }
    }

    if (labelFilters.length > 0) {
      // OR semantics within the filter: keep issues that carry any of the
      // selected labels. Matches existing priority / project multi-select.
      const issueLabels = issue.labels;
      if (!issueLabels || issueLabels.length === 0) return false;
      if (!issueLabels.some((l) => labelFilters.includes(l.id))) return false;
    }

    if (sprintFilters.length > 0) {
      // OR semantics, mirroring labels. An issue belongs to at most one sprint
      // (`sprint_id`), so this keeps issues whose sprint is in the selected set.
      if (!issue.sprint_id || !sprintFilters.includes(issue.sprint_id))
        return false;
    }

    if (bitrixGroupFilters && bitrixGroupFilters.length > 0) {
      // Metadata is untyped JSON from the API, so read defensively: a non-string
      // value (or a non-Bitrix issue) simply fails the filter instead of throwing.
      const groupId = (issue.metadata as Record<string, unknown> | null | undefined)
        ?.bitrix_group_id;
      if (typeof groupId !== "string" || !bitrixGroupFilters.includes(groupId))
        return false;
    }

    return true;
  });
}
