"use client";

import { Code2, MessageSquareCode } from "lucide-react";
import { type WorkMode } from "@agora/core/issues/work-mode";
import { cn } from "@agora/ui/lib/utils";

// Prominent two-option segmented control for an issue's work mode:
//   Prompts (full_pipeline) — hand the agent scoped slice-actions.
//   Editor  (in_editor)     — co-code live in the embedded editor.
// Plain strings to match the sibling repo / editor sections.

const OPTIONS: { value: WorkMode; label: string; Icon: typeof Code2 }[] = [
  { value: "full_pipeline", label: "Prompts", Icon: MessageSquareCode },
  { value: "in_editor", label: "Editor", Icon: Code2 },
];

export function WorkModeSwitch({
  value,
  onChange,
  disabled,
}: {
  value: WorkMode;
  onChange: (mode: WorkMode) => void;
  disabled?: boolean;
}) {
  return (
    <div
      role="tablist"
      aria-label="Work mode"
      className="grid grid-cols-2 gap-1 rounded-lg bg-muted/60 p-0.5"
    >
      {OPTIONS.map(({ value: v, label, Icon }) => {
        const selected = v === value;
        return (
          <button
            key={v}
            type="button"
            role="tab"
            aria-selected={selected}
            disabled={disabled}
            onClick={() => {
              if (v !== value) onChange(v);
            }}
            className={cn(
              "flex items-center justify-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium transition-colors disabled:opacity-50",
              selected
                ? "bg-background text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="size-3.5 shrink-0" />
            {label}
          </button>
        );
      })}
    </div>
  );
}
