"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import { issueKeys } from "@agora/core/issues/queries";
import { PropertyPicker, PickerItem, PickerEmpty } from "./property-picker";
import { useT } from "../../../i18n";

// Batch "Move to sprint" picker for the selection toolbar. Lists every
// non-completed sprint in the workspace (grouped visually by project) and moves
// the selected issues onto the chosen sprint via POST /api/issues/batch-sprint.
// Sprints are project-scoped, so issues in a different project than the sprint
// are skipped server-side; the toast reports how many actually moved.
export function SprintPicker({
  issueIds,
  trigger: customTrigger,
  triggerRender,
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
  align,
  onDone,
}: {
  issueIds: string[];
  trigger?: React.ReactNode;
  triggerRender?: React.ReactElement;
  open?: boolean;
  onOpenChange?: (v: boolean) => void;
  align?: "start" | "center" | "end";
  onDone?: () => void;
}) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { t } = useT("issues");
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = controlledOnOpenChange ?? setInternalOpen;

  const { data: sprints = [] } = useQuery({
    queryKey: ["workspace-sprints", wsId],
    queryFn: () => api.listWorkspaceSprints(),
    enabled: open,
    staleTime: 60_000,
  });

  const move = useMutation({
    mutationFn: (sprintId: string) => api.batchSetIssueSprint(issueIds, sprintId),
    onSuccess: (res) => {
      toast.success(
        t(($) => $.batch.sprint_moved, { count: res.moved, total: issueIds.length }),
      );
      qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
      onDone?.();
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : t(($) => $.batch.update_failed)),
  });

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-60"
      align={align}
      triggerRender={triggerRender}
      trigger={customTrigger}
    >
      {sprints.length === 0 ? (
        <PickerEmpty />
      ) : (
        sprints.map((s) => (
          <PickerItem
            key={s.id}
            selected={false}
            onClick={() => {
              move.mutate(s.id);
              setOpen(false);
            }}
          >
            <span className="truncate">{s.name}</span>
            <span className="ml-auto shrink-0 truncate pl-2 text-xs text-muted-foreground">
              {s.project_title}
            </span>
          </PickerItem>
        ))
      )}
    </PropertyPicker>
  );
}
