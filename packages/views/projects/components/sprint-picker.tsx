"use client";

import { useQuery } from "@tanstack/react-query";
import { Check, Rocket, X } from "lucide-react";
import { toast } from "sonner";
import {
  sprintListByProjectOptions,
  issueSprintOptions,
} from "@agora/core/sprints/queries";
import { useSetIssueSprint } from "@agora/core/sprints/mutations";
import { SPRINT_STATUS_CONFIG } from "@agora/core/sprints/config";
import { useWorkspaceId } from "@agora/core/hooks";
import { cn } from "@agora/ui/lib/utils";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  DropdownMenuSeparator,
} from "@agora/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n";

/**
 * Sprint picker for an issue, mirroring {@link ProjectPicker}. Unlike the
 * project picker (which rides the issue-update path via `onUpdate`), sprint
 * assignment has its own endpoints, so this component owns its mutation
 * (`useSetIssueSprint`) and derives the current sprint via `issueSprintOptions`
 * — the issue object carries no `sprint_id`.
 *
 * Sprints are project-scoped: with no project there are no sprints to pick, so
 * the trigger shows a hint and the menu is effectively empty.
 */
export function SprintPicker({
  issueId,
  projectId,
  triggerRender,
  align = "start",
  defaultOpen = false,
}: {
  issueId: string;
  projectId: string | null;
  triggerRender?: React.ReactElement;
  align?: "start" | "center" | "end";
  /** Open the dropdown on first mount (progressive-disclosure sidebars). */
  defaultOpen?: boolean;
}) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const { data: sprints = [] } = useQuery({
    ...sprintListByProjectOptions(wsId, projectId ?? ""),
    enabled: !!projectId,
  });
  const { data: currentSprint } = useQuery(
    issueSprintOptions(wsId, issueId, projectId),
  );
  const setSprint = useSetIssueSprint(issueId);

  const assign = (sprintId: string | null) => {
    setSprint.mutate(sprintId, {
      onError: (err) =>
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.sprint_picker.toast_assign_failed),
        ),
    });
  };

  const currentId = currentSprint?.id ?? null;

  return (
    <DropdownMenu defaultOpen={defaultOpen}>
      <DropdownMenuTrigger
        className={
          triggerRender
            ? undefined
            : "flex items-center gap-1.5 cursor-pointer rounded px-1 -mx-1 hover:bg-accent/30 transition-colors overflow-hidden"
        }
        render={triggerRender}
      >
        {currentSprint ? (
          <span
            className={cn(
              "size-2 shrink-0 rounded-full",
              SPRINT_STATUS_CONFIG[currentSprint.status]?.dotColor ??
                SPRINT_STATUS_CONFIG.planned.dotColor,
            )}
            aria-hidden
          />
        ) : (
          <Rocket className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        )}
        <span className="truncate">
          {currentSprint ? currentSprint.name : t(($) => $.sprint_picker.no_sprint)}
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="w-52">
        {!projectId && (
          <div className="px-2 py-1.5 text-xs text-muted-foreground">
            {t(($) => $.sprint_picker.no_project)}
          </div>
        )}
        {projectId &&
          sprints.map((s) => (
            <DropdownMenuItem key={s.id} onClick={() => assign(s.id)}>
              <span
                className={cn(
                  "size-2 shrink-0 rounded-full",
                  SPRINT_STATUS_CONFIG[s.status]?.dotColor ??
                    SPRINT_STATUS_CONFIG.planned.dotColor,
                )}
                aria-hidden
              />
              <span className="truncate">{s.name}</span>
              {s.id === currentId && <Check className="ml-auto h-3.5 w-3.5 shrink-0" />}
            </DropdownMenuItem>
          ))}
        {projectId && sprints.length > 0 && currentId && <DropdownMenuSeparator />}
        {projectId && currentId && (
          <DropdownMenuItem onClick={() => assign(null)}>
            <X className="h-3.5 w-3.5 text-muted-foreground" />
            {t(($) => $.sprint_picker.remove)}
          </DropdownMenuItem>
        )}
        {projectId && sprints.length === 0 && (
          <div className="px-2 py-1.5 text-xs text-muted-foreground">
            {t(($) => $.sprint_picker.empty)}
          </div>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
