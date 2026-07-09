"use client";

import { useState, type ReactNode } from "react";
import { ChevronRight, Bot } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { type AgentProtocol } from "./agent-protocol";

// Human-facing render of an agent-protocol comment: a one-line headline (who
// was asked to do what), a QA pipeline diagram for run_qa, and the full machine
// prompt collapsed behind a disclosure. The raw prompt is what the agent reads;
// the human never has to.

// The QA gate's fixed stages as a compact horizontal stepper — shows the human
// WHAT the gate will do without reading the prompt. Static (pre-run): it's the
// plan, not live results (the verdict comment carries actual pass/fail). Static
// selectors (no dynamic key) so the i18n selector API typechecks.
function QAPipeline() {
  const { t } = useT("issues");
  const stages = [
    t(($) => $.agent_protocol.qa_stage.baseline),
    t(($) => $.agent_protocol.qa_stage.checks),
    t(($) => $.agent_protocol.qa_stage.smoke),
    t(($) => $.agent_protocol.qa_stage.tests),
    t(($) => $.agent_protocol.qa_stage.verdict),
  ];
  return (
    <div className="mt-2 flex flex-wrap items-center gap-1 pl-1">
      {stages.map((label, i) => (
        <div key={label} className="flex items-center gap-1">
          <span className="rounded-md border border-border bg-muted/40 px-2 py-0.5 text-[11px] text-foreground/70">
            {label}
          </span>
          {i < stages.length - 1 && (
            <ChevronRight aria-hidden className="size-3 shrink-0 text-muted-foreground/40" />
          )}
        </div>
      ))}
    </div>
  );
}

export function AgentProtocolComment({
  protocol,
  renderBody,
}: {
  protocol: AgentProtocol;
  /** Renders the raw instruction markdown — supplied by the caller so this
   * component reuses the thread's existing markdown renderer. */
  renderBody: (raw: string) => ReactNode;
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const who = protocol.agentName || t(($) => $.agent_protocol.an_agent);
  let line: string;
  switch (protocol.kind) {
    case "run_qa": line = t(($) => $.agent_protocol.headline.run_qa); break;
    case "write_tests": line = t(($) => $.agent_protocol.headline.write_tests); break;
    case "gen_tests": line = t(($) => $.agent_protocol.headline.gen_tests); break;
    case "write_docs": line = t(($) => $.agent_protocol.headline.write_docs); break;
    case "review": line = t(($) => $.agent_protocol.headline.review); break;
    case "design": line = t(($) => $.agent_protocol.headline.design); break;
    default: line = t(($) => $.agent_protocol.headline.delegate); break;
  }
  return (
    <div className="rounded-lg border border-border/60 bg-muted/20 px-3 py-2">
      <div className="flex items-start gap-2">
        <Bot aria-hidden className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 text-[13px] leading-snug text-foreground/85">
          {line.replace("{{name}}", who)}
        </span>
      </div>
      {protocol.kind === "run_qa" && <QAPipeline />}
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="mt-1.5 inline-flex items-center gap-1 text-[11px] text-muted-foreground/70 transition-colors hover:text-muted-foreground"
      >
        <ChevronRight
          aria-hidden
          className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")}
        />
        {open
          ? t(($) => $.agent_protocol.hide_details)
          : t(($) => $.agent_protocol.show_details)}
      </button>
      {open && (
        <div className="mt-1.5 border-t border-border/50 pt-2 text-sm leading-relaxed text-foreground/70">
          {renderBody(protocol.instruction)}
        </div>
      )}
    </div>
  );
}
