"use client";

import { useState } from "react";
import { Zap, Hand } from "lucide-react";
import { useSetIssueMetadataKey, useDeleteIssueMetadataKey } from "@agora/core/issues/mutations";
import { PropertyPicker, PickerItem } from "./pickers/property-picker";
import { useT } from "../../i18n";

/**
 * PipelineModePicker toggles a per-issue pipeline_mode between "auto" (the
 * QA/review/merge reflexes fire — the default) and "manual" (the orchestrator
 * drives each stage itself, woken at every gate). Auto CLEARS the key rather
 * than storing "auto", so the default stays represented by absence — matching
 * how the backend resolver reads it.
 */
export function PipelineModePicker({
  issueId,
  mode,
  align = "start",
}: {
  issueId: string;
  mode: string | null;
  align?: "start" | "center" | "end";
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const setMode = useSetIssueMetadataKey();
  const clearMode = useDeleteIssueMetadataKey();
  const isManual = mode?.toLowerCase() === "manual";

  const choose = (manual: boolean) => {
    if (manual) setMode.mutate({ issueId, key: "pipeline_mode", value: "manual" });
    else clearMode.mutate({ issueId, key: "pipeline_mode" });
    setOpen(false);
  };

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-52"
      align={align}
      trigger={
        <span className="flex items-center gap-1.5">
          {isManual ? <Hand className="size-3" /> : <Zap className="size-3" />}
          <span>{isManual ? t(($) => $.detail.pipeline_manual) : t(($) => $.detail.pipeline_auto)}</span>
        </span>
      }
    >
      <PickerItem selected={!isManual} onClick={() => choose(false)} tooltip={t(($) => $.detail.pipeline_auto_desc)}>
        <Zap className="size-3.5 text-muted-foreground" />
        <span>{t(($) => $.detail.pipeline_auto)}</span>
      </PickerItem>
      <PickerItem selected={isManual} onClick={() => choose(true)} tooltip={t(($) => $.detail.pipeline_manual_desc)}>
        <Hand className="size-3.5 text-muted-foreground" />
        <span>{t(($) => $.detail.pipeline_manual)}</span>
      </PickerItem>
    </PropertyPicker>
  );
}
