"use client";

import { useT } from "../../i18n";
import type { FollowUpId } from "../lib/follow-ups";

interface FollowUpChipsProps {
  ids: readonly FollowUpId[];
  /** Prefills the composer. Deliberately does NOT send — the chip is a
   *  starting point the user can edit, not a decision made for them. */
  onPick: (content: string) => void;
}

/**
 * Up to three quiet suggestions under a finished reply. Derived from the tool
 * the run called (lib/follow-ups.ts), never from a second model call.
 */
export function FollowUpChips({ ids, onPick }: FollowUpChipsProps) {
  const { t } = useT("assistant");
  if (ids.length === 0) return null;

  return (
    <div
      role="group"
      aria-label={t(($) => $.follow_ups.label)}
      className="mx-auto -mt-2 mb-4 flex w-full max-w-2xl flex-wrap gap-1.5 px-4"
    >
      {ids.map((id) => {
        const label = t(($) => $.follow_ups[id]);
        return (
          <button
            key={id}
            type="button"
            onClick={() => onPick(label)}
            className="inline-flex max-w-full cursor-pointer items-center rounded-full border px-3 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <span className="truncate">{label}</span>
          </button>
        );
      })}
    </div>
  );
}
