"use client";

import { useState } from "react";
import { Bug, ChevronDown, Hammer, ListChecks, WandSparkles } from "lucide-react";
import type { AgentRunMode } from "@agora/core/types";
import { PropertyPicker, PickerItem } from "./pickers/property-picker";
import { useT } from "../../i18n";

const MODES: Array<{
  value: AgentRunMode;
  icon: typeof WandSparkles;
  labelKey: "run_mode_auto" | "run_mode_debug" | "run_mode_plan" | "run_mode_build";
  descriptionKey: "run_mode_auto_desc" | "run_mode_debug_desc" | "run_mode_plan_desc" | "run_mode_build_desc";
}> = [
  { value: "auto", icon: WandSparkles, labelKey: "run_mode_auto", descriptionKey: "run_mode_auto_desc" },
  { value: "debug", icon: Bug, labelKey: "run_mode_debug", descriptionKey: "run_mode_debug_desc" },
  { value: "plan", icon: ListChecks, labelKey: "run_mode_plan", descriptionKey: "run_mode_plan_desc" },
  { value: "build", icon: Hammer, labelKey: "run_mode_build", descriptionKey: "run_mode_build_desc" },
];

/**
 * Per-submit agent behavior. The selected value travels with the comment and
 * is snapshotted onto each task it triggers; it is not issue metadata and does
 * not alter type:* labels or the agent's defaults.
 */
export function RunModePicker({
  value,
  onChange,
}: {
  value: AgentRunMode;
  onChange: (value: AgentRunMode) => void;
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const selected = MODES.find((mode) => mode.value === value) ?? MODES[0]!;
  const SelectedIcon = selected.icon;
  const selectedLabel = t(($) => $.comment[selected.labelKey]);

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-64"
      align="end"
      side="top"
      tooltip={t(($) => $.comment.run_mode_tooltip)}
      triggerRender={
        <button
          type="button"
          aria-label={t(($) => $.comment.run_mode_aria, { mode: selectedLabel })}
          className="flex h-7 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        />
      }
      trigger={
        <>
          <SelectedIcon className="size-3.5" />
          <span>{selectedLabel}</span>
          <ChevronDown className="size-3" />
        </>
      }
    >
      {MODES.map((mode) => {
        const Icon = mode.icon;
        return (
          <PickerItem
            key={mode.value}
            selected={mode.value === value}
            onClick={() => {
              onChange(mode.value);
              setOpen(false);
            }}
            tooltip={t(($) => $.comment[mode.descriptionKey])}
          >
            <Icon className="size-3.5 text-muted-foreground" />
            <span className="min-w-0">
              <span className="block font-medium text-foreground">{t(($) => $.comment[mode.labelKey])}</span>
              <span className="block truncate text-xs text-muted-foreground">{t(($) => $.comment[mode.descriptionKey])}</span>
            </span>
          </PickerItem>
        );
      })}
    </PropertyPicker>
  );
}
