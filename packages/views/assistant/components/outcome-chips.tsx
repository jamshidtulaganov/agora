"use client";

import { useState } from "react";
import { AlertTriangle, CheckCircle2 } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import type { Receipt, UncertainOutcome } from "../lib/operation";
import { targetLabel } from "../lib/operation";
import { humanizeToolName } from "../lib/tool-summary";

/**
 * A mutation that carried a receipt: what it did, to what, where to look, and
 * what else changed because of it (docs/agora-assistant-final-plan.md §3
 * "Make outcomes recoverable"). Replaces the generic ToolChip only when the
 * receipt parsed — anything else still gets the chip.
 *
 * Effects collapse to one line by default: a receipt is a glance, not a
 * report, and a five-line side-effect list in the middle of a transcript
 * buries the assistant's actual answer.
 */
export function ReceiptChip({ receipt }: { receipt: Receipt }) {
  const target = targetLabel(receipt.target);
  // The server sends the tool name as the action ("create_issue"); humanizing
  // it here keeps the headline identical to the plain ToolChip's, so the two
  // chip kinds read as one family. A server that sends a phrase is unchanged.
  const action = humanizeToolName(receipt.action);

  return (
    <div className="ml-8 flex w-full max-w-md flex-col gap-1 rounded-md border border-border bg-card px-2.5 py-1.5 text-xs">
      <div className="flex min-w-0 items-center gap-1.5">
        <CheckCircle2 className="size-3 shrink-0 text-success" />
        <span className="shrink-0 font-medium">{action}</span>
        {target && <span className="truncate text-muted-foreground">{target}</span>}
      </div>

      {receipt.links.length > 0 && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          {receipt.links.map((link) => (
            <AppLink
              key={`${link.href}-${link.label}`}
              href={link.href}
              className="max-w-full truncate text-brand underline-offset-2 hover:underline"
            >
              {link.label}
            </AppLink>
          ))}
        </div>
      )}

      {receipt.effects.length > 0 && <ReceiptEffects effects={receipt.effects} />}
    </div>
  );
}

function ReceiptEffects({ effects }: { effects: string[] }) {
  const { t } = useT("assistant");
  const [isExpanded, setExpanded] = useState(false);
  const hidden = effects.length - 1;

  return (
    <div className="text-muted-foreground">
      <button
        type="button"
        aria-expanded={isExpanded}
        aria-label={t(($) => $.receipt.effects_toggle)}
        onClick={() => setExpanded((open) => !open)}
        className={cn(
          "flex w-full min-w-0 cursor-pointer items-baseline gap-1 text-left",
          "hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
        )}
      >
        {isExpanded ? (
          <span className="underline-offset-2 hover:underline">
            {t(($) => $.receipt.effects_hide)}
          </span>
        ) : (
          <>
            <span className="truncate">{effects[0]}</span>
            {hidden > 0 && (
              <span className="shrink-0 whitespace-nowrap underline-offset-2 hover:underline">
                {t(($) => $.receipt.effects_more, { count: hidden })}
              </span>
            )}
          </>
        )}
      </button>
      {isExpanded && (
        <ul className="mt-1 flex list-disc flex-col gap-0.5 pl-4">
          {effects.map((effect, index) => (
            <li key={`${index}-${effect}`} className="break-words">
              {effect}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * An interrupted or ambiguous mutation: it may or may not have taken effect.
 * Deliberately NOT styled as an error — nothing failed, and nothing is known
 * to have succeeded. The plan forbids blindly replaying these, so the chip's
 * whole job is to send the user to look (§2 "Uncertain effects").
 */
export function UncertainChip({ outcome }: { outcome: UncertainOutcome }) {
  const { t } = useT("assistant");

  return (
    <div
      role="status"
      className="ml-8 flex w-full max-w-md items-start gap-1.5 rounded-md border border-warning/30 bg-warning/10 px-2.5 py-1.5 text-xs text-warning"
    >
      <AlertTriangle className="mt-px size-3 shrink-0" />
      <div className="min-w-0 flex-1">
        <span className="font-medium">{t(($) => $.uncertain.title)}</span>{" "}
        <span className="break-words">
          {outcome.inspect || t(($) => $.uncertain.hint)}
        </span>
      </div>
    </div>
  );
}
