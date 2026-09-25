"use client";

import { toggleHiddenNavKey } from "@agora/core/sidebar";
import { RadioGroup, RadioGroupItem } from "@agora/ui/components/ui/radio-group";
import { cn } from "@agora/ui/lib/utils";
import type { NavKey } from "../layout/nav-items";
import { NavVisibilityList } from "../settings/components/nav-visibility-list";
import { useT } from "../i18n";
import { SIDEBAR_PRESETS, matchSidebarPreset, presetHiddenNav, type SidebarPreset } from "./presets";

/**
 * Step 2: the sidebar members get. Two presets as the one obvious choice,
 * with every item's switch below for anyone who wants to adjust it. The
 * draft lives in the wizard and is saved when the person moves on.
 */
export function SidebarStep({
  hidden,
  onChange,
  disabled,
}: {
  hidden: string[];
  onChange: (next: string[]) => void;
  disabled: boolean;
}) {
  const { t } = useT("department-setup");
  const preset = matchSidebarPreset(hidden);

  const toggle = (key: NavKey, visible: boolean) => {
    const next = toggleHiddenNavKey(hidden, key, !visible);
    if (next !== hidden) onChange(next);
  };

  return (
    <div className="space-y-6">
      <RadioGroup
        aria-label={t(($) => $.sidebar.presets_label)}
        value={preset}
        onValueChange={(value: SidebarPreset | null) => {
          if (value) onChange(presetHiddenNav(value));
        }}
        disabled={disabled}
        className="gap-2 sm:grid-cols-2"
      >
        {SIDEBAR_PRESETS.map((value) => (
          <label
            key={value}
            data-testid={`setup-preset-${value}`}
            data-selected={preset === value}
            className={cn(
              "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-muted/50",
              preset === value && "border-primary",
            )}
          >
            <RadioGroupItem value={value} className="mt-0.5" />
            <span className="min-w-0">
              <span className="block text-sm font-medium">{t(($) => $.sidebar[value].label)}</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">
                {t(($) => $.sidebar[value].description)}
              </span>
            </span>
          </label>
        ))}
      </RadioGroup>

      <NavVisibilityList hidden={hidden} onToggle={toggle} disabled={disabled} />

      <p className="text-xs text-muted-foreground">{t(($) => $.sidebar.note)}</p>
    </div>
  );
}
