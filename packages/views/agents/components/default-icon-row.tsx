"use client";

import { cn } from "@agora/ui/lib/utils";
import { DEFAULT_AGENT_ICONS } from "./default-agent-icons";
import { useT } from "../../i18n";

/**
 * A row of one-click preset avatars shown under the avatar/name row in the
 * create-agent form. Picking one sets avatar_url straight away, so a new agent
 * gets a distinct icon without uploading a file. The currently-selected preset
 * (if the avatar matches one) is ringed.
 */
export function DefaultIconRow({
  value,
  onChange,
}: {
  value: string | null;
  onChange: (url: string) => void;
}) {
  const { t } = useT("agents");
  return (
    <div>
      <div className="text-xs text-muted-foreground">
        {t(($) => $.create_dialog.avatar.suggested_label)}
      </div>
      <div className="mt-1.5 flex flex-wrap gap-2">
        {DEFAULT_AGENT_ICONS.map((icon, i) => {
          const active = value === icon;
          return (
            <button
              key={i}
              type="button"
              onClick={() => onChange(icon)}
              aria-label={t(($) => $.create_dialog.avatar.suggested_aria, { n: i + 1 })}
              aria-pressed={active}
              className={cn(
                "h-8 w-8 shrink-0 overflow-hidden rounded-lg border outline-none transition-all",
                "focus-visible:ring-2 focus-visible:ring-ring",
                active
                  ? "border-primary ring-2 ring-primary/40"
                  : "border-border hover:border-foreground/30",
              )}
            >
              <img src={icon} alt="" className="h-full w-full object-cover" />
            </button>
          );
        })}
      </div>
    </div>
  );
}
