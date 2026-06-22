"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CalendarClock, ChevronRight, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { sprintListByProjectOptions } from "@agora/core/sprints/queries";
import { useDeleteSprint } from "@agora/core/sprints/mutations";
import { SPRINT_STATUS_CONFIG } from "@agora/core/sprints/config";
import { useModalStore } from "@agora/core/modals";
import { useWorkspaceId } from "@agora/core/hooks";
import { formatDateOnly } from "@agora/core/issues/date";
import type { Sprint } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { Button } from "@agora/ui/components/ui/button";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@agora/ui/components/ui/tooltip";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@agora/ui/components/ui/alert-dialog";
import { useT } from "../../i18n";
import { useSprintStatusLabels } from "./sprint-labels";

function formatSprintDates(
  sprint: Sprint,
  noDates: string,
  range: (start: string, end: string) => string,
): string {
  if (!sprint.start_date && !sprint.end_date) return noDates;
  const fmt = (d: string | null) =>
    d ? formatDateOnly(d, { month: "short", day: "numeric" }, "en-US") : "…";
  return range(fmt(sprint.start_date), fmt(sprint.end_date));
}

/**
 * Project Sprints sidebar section. Mirrors ProjectResourcesSection's shape:
 * a collapsible header with an add affordance, a list of rows, and an empty
 * state. Each row shows the sprint name, a status dot, and the date range,
 * with a delete action behind a confirmation dialog. Create flows through the
 * shared modal store (`create-sprint`, carrying the project id).
 *
 * Phase 2b (deferred — not built):
 *   - sprint board/backlog view (clicking a sprint row should open it; rows
 *     are display-only for now)
 *   - filter-by-sprint in the project issues header
 *   - per-project "sprint mode" toggle
 */
export function ProjectSprintsSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const statusLabels = useSprintStatusLabels();
  const [open, setOpen] = useState(true);
  const [pendingDelete, setPendingDelete] = useState<Sprint | null>(null);

  const { data: sprints = [] } = useQuery(
    sprintListByProjectOptions(wsId, projectId),
  );
  const deleteSprint = useDeleteSprint(projectId);

  const openCreate = () =>
    useModalStore.getState().open("create-sprint", { project_id: projectId });

  const handleDelete = () => {
    if (!pendingDelete) return;
    const sprint = pendingDelete;
    setPendingDelete(null);
    deleteSprint.mutate(sprint.id, {
      onSuccess: () => toast.success(t(($) => $.sprints.toast_deleted)),
      onError: (err) =>
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.sprints.toast_delete_failed),
        ),
    });
  };

  return (
    <div>
      <div className="mb-2 flex items-center justify-between gap-1">
        <button
          type="button"
          className={cn(
            "flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70",
            open ? "" : "text-muted-foreground hover:text-foreground",
          )}
          onClick={() => setOpen(!open)}
        >
          {t(($) => $.sprints.section_header)}
          <ChevronRight
            className={cn(
              "!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
              open && "rotate-90",
            )}
          />
        </button>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="ghost"
                size="icon-sm"
                className="text-muted-foreground"
                onClick={openCreate}
              >
                <Plus />
              </Button>
            }
          />
          <TooltipContent side="bottom">{t(($) => $.sprints.new_sprint)}</TooltipContent>
        </Tooltip>
      </div>

      {open && (
        <div className="space-y-1 pl-2">
          {sprints.length === 0 ? (
            <p className="px-2 py-1.5 text-xs text-muted-foreground">
              {t(($) => $.sprints.empty)}
            </p>
          ) : (
            sprints.map((sprint) => {
              const cfg = SPRINT_STATUS_CONFIG[sprint.status];
              return (
                <div
                  key={sprint.id}
                  className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-accent/50 transition-colors"
                >
                  <span
                    className={cn("size-2 shrink-0 rounded-full", cfg.dotColor)}
                    aria-hidden
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-xs font-medium">{sprint.name}</span>
                      <span
                        className={cn(
                          "shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium",
                          cfg.badgeBg,
                          cfg.badgeText,
                        )}
                      >
                        {statusLabels[sprint.status]}
                      </span>
                    </div>
                    <div className="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground">
                      <CalendarClock className="size-3 shrink-0" />
                      <span className="truncate">
                        {formatSprintDates(
                          sprint,
                          t(($) => $.sprints.no_dates),
                          (start, end) => t(($) => $.sprints.date_range, { start, end }),
                        )}
                      </span>
                    </div>
                  </div>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <button
                          type="button"
                          onClick={() => setPendingDelete(sprint)}
                          className="shrink-0 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100"
                          aria-label={t(($) => $.sprints.delete_action)}
                        >
                          <Trash2 className="size-3.5" />
                        </button>
                      }
                    />
                    <TooltipContent side="bottom">{t(($) => $.sprints.delete_action)}</TooltipContent>
                  </Tooltip>
                </div>
              );
            })
          )}
        </div>
      )}

      <AlertDialog open={!!pendingDelete} onOpenChange={(v) => { if (!v) setPendingDelete(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.sprints.delete_dialog_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.sprints.delete_dialog_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.sprints.delete_dialog_cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-destructive text-white hover:bg-destructive/90"
            >
              {t(($) => $.sprints.delete_dialog_confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
