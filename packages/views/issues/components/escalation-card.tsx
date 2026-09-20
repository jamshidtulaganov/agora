"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { HandHelping, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { issueEscalationsOptions } from "@agora/core/issues/queries";
import { useResolveEscalation } from "@agora/core/issues/mutations";
import { useActorName } from "@agora/core/workspace/hooks";
import type { Escalation } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { useT } from "../../i18n";

// The escalation card — an agent stopped and is waiting on a person
// (docs/orchestration-upgrade-plan.md §B1).
//
// This is the one surface where a stopped run becomes a decision a human can
// make in seconds. Everything about it is built for that: the ask is the
// headline, the answer box is already open, and picking an option IS the
// answer (free text and a chosen option go through the same field, which is
// why "redirect the agent" needs no separate control).
//
// Renders NOTHING when there is no open escalation — including when the
// response failed to parse, whose fallback is an empty list. An issue nobody
// escalated and an issue whose escalation endpoint drifted look identical,
// which is the correct degradation for a panel that is normally absent.

export function EscalationCard({ issueId }: { issueId: string }) {
  const { data: escalations = [] } = useQuery(issueEscalationsOptions(issueId));
  const open = escalations.find((e) => e.status === "open") ?? null;

  if (!open || !open.id) return null;
  return <OpenEscalation key={open.id} issueId={issueId} escalation={open} />;
}

function OpenEscalation({ issueId, escalation }: { issueId: string; escalation: Escalation }) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const [answer, setAnswer] = useState("");
  const resolve = useResolveEscalation();

  const agentName = escalation.agent_id
    ? getActorName("agent", escalation.agent_id)
    : t(($) => $.escalation.an_agent);

  const submit = (text: string) => {
    const trimmed = text.trim();
    if (!trimmed || resolve.isPending) return;
    resolve.mutate(
      { escalationId: escalation.id, issueId, answer: trimmed },
      {
        onSuccess: () => {
          setAnswer("");
          toast.success(t(($) => $.escalation.answered_toast));
        },
        // 409 means someone else answered first — a normal outcome on a
        // shared queue, not an error the user caused. The list refetches on
        // settle either way, so the card simply disappears.
        onError: () => toast.error(t(($) => $.escalation.answer_failed)),
      },
    );
  };

  return (
    <section className="mt-5 rounded-lg border border-amber-500/40 bg-amber-500/5 p-4">
      <div className="flex items-start gap-2.5">
        <HandHelping className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-500" />
        <div className="min-w-0 flex-1">
          <p className="text-xs font-medium text-amber-700 dark:text-amber-500">
            {t(($) => $.escalation.heading, { name: agentName })}
          </p>
          <p className="mt-1 text-sm font-medium leading-snug break-words">
            {escalation.prompt}
          </p>

          {escalation.detail && (
            <p className="mt-2 whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
              <span className="font-medium">{t(($) => $.escalation.tried)}</span>{" "}
              {escalation.detail}
            </p>
          )}

          {/* Options are a shortcut, never a constraint: one tap answers, and
              the free-text box below stays available for anyone whose real
              answer is "neither — do this instead". */}
          {escalation.options.length > 0 && (
            <div className="mt-3 flex flex-wrap gap-1.5">
              {escalation.options.map((option) => (
                <Button
                  key={option}
                  size="sm"
                  variant="outline"
                  disabled={resolve.isPending}
                  onClick={() => submit(option)}
                  className="h-7 max-w-full"
                >
                  <span className="truncate">{option}</span>
                </Button>
              ))}
            </div>
          )}

          <div className="mt-3">
            <Textarea
              value={answer}
              onChange={(e) => setAnswer(e.target.value)}
              placeholder={t(($) => $.escalation.answer_placeholder)}
              rows={2}
              disabled={resolve.isPending}
              className="min-h-16 resize-y bg-background text-sm"
              // Cmd/Ctrl+Enter submits — the same muscle memory as every
              // other composer in the product.
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                  e.preventDefault();
                  submit(answer);
                }
              }}
            />
            <div className="mt-2 flex items-center justify-between gap-3">
              <p className="min-w-0 text-[11px] leading-tight text-muted-foreground">
                {t(($) => $.escalation.resume_hint)}
              </p>
              <Button
                size="sm"
                className="h-7 shrink-0"
                disabled={!answer.trim() || resolve.isPending}
                onClick={() => submit(answer)}
              >
                {resolve.isPending && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
                {t(($) => $.escalation.send_answer)}
              </Button>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
