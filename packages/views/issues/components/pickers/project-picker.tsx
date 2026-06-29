"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { UpdateIssueRequest } from "@agora/core/types";
import { projectListOptions } from "@agora/core/projects/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { ProjectIcon } from "../../../projects/components/project-icon";
import { PropertyPicker, PickerItem, PickerEmpty } from "./property-picker";

// Batch "Move to project" picker for the selection toolbar — lists the
// workspace's projects and emits onUpdate({ project_id }) so a multi-select can
// be re-filed in one call (BatchUpdateIssues). Mirrors the other batch pickers
// (status/priority/assignee). The inline single-issue variant lives in
// projects/components/project-picker; this one is the toolbar variant built on
// the shared PropertyPicker shell with a controllable open state.
export function ProjectPicker({
  onUpdate,
  trigger: customTrigger,
  triggerRender,
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
  align,
}: {
  onUpdate: (updates: Partial<UpdateIssueRequest>) => void;
  trigger?: React.ReactNode;
  triggerRender?: React.ReactElement;
  open?: boolean;
  onOpenChange?: (v: boolean) => void;
  align?: "start" | "center" | "end";
}) {
  const wsId = useWorkspaceId();
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = controlledOnOpenChange ?? setInternalOpen;

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-52"
      align={align}
      triggerRender={triggerRender}
      trigger={customTrigger}
    >
      {projects.length === 0 ? (
        <PickerEmpty />
      ) : (
        projects.map((p) => (
          <PickerItem
            key={p.id}
            selected={false}
            onClick={() => {
              onUpdate({ project_id: p.id });
              setOpen(false);
            }}
          >
            <ProjectIcon project={p} size="md" className="mr-1" />
            <span className="truncate">{p.title}</span>
          </PickerItem>
        ))
      )}
    </PropertyPicker>
  );
}
