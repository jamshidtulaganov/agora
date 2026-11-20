"use client";

import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import { Reveal } from "./reveal";
import {
  SectionIntro,
  StepNumber,
  containerClassName,
  primaryButtonClassName,
  sectionClassName,
  ui,
  useTicker,
} from "./shared";

export function AgentSetupSection() {
  const { t } = useLocale();
  const a = t.agentSetup;

  return (
    <section id="agent" className={cn(sectionClassName, "border-t", ui.surface, ui.line)}>
      <div className={cn(containerClassName, "flex flex-wrap items-center gap-x-[72px] gap-y-12")}>
        <Reveal className="flex min-w-0 flex-[1_1_400px] flex-col gap-6">
          <SectionIntro label={a.label} title={a.title} />
          <ol className="m-0 flex list-none flex-col gap-[18px] p-0">
            {a.steps.map((s, i) => (
              <li key={s.title} className="flex gap-4">
                <StepNumber n={i + 1} />
                <span>
                  <span className="font-semibold">{s.title}</span>{" "}
                  <span className={ui.muted}>{s.body}</span>
                </span>
              </li>
            ))}
          </ol>
        </Reveal>

        <Reveal
          delay={120}
          className={cn(
            "flex min-w-0 flex-[1_1_380px] flex-col gap-4 rounded-[18px] border p-5 sm:p-6",
            ui.card,
            ui.lineStrong,
            ui.shadow,
          )}
        >
          <span className="text-[18px] font-semibold">{a.card.title}</span>
          <Field label={a.card.nameLabel}>
            <span className={cn("block rounded-[10px] border px-3.5 py-2.5", ui.inset, ui.line)}>
              {a.card.nameValue}
            </span>
          </Field>
          <Field label={a.card.jobLabel}>
            <span
              className={cn(
                "block min-h-24 rounded-[10px] border border-[#2563EB] px-3.5 py-2.5",
                ui.inset,
              )}
            >
              {a.card.jobValue}
              <span
                aria-hidden
                className="ml-0.5 inline-block h-[18px] w-0.5 animate-pulse bg-[#5B8DEF] align-text-bottom"
              />
            </span>
          </Field>
          <Field label={a.card.docsLabel}>
            <Chips items={a.card.docs} />
          </Field>
          <Field label={a.card.toolsLabel}>
            <Chips items={["Zoho Desk"]} />
          </Field>
          <span className={cn(primaryButtonClassName("md"), "self-start")}>
            {a.card.createAgent}
          </span>
        </Reveal>
      </div>
    </section>
  );
}

export function AutomationSection() {
  const { t } = useLocale();
  const a = t.automation;
  const active = useTicker(3400) % 3;
  const rows = [
    { k: a.when, v: a.whenValue },
    { k: a.if, v: a.ifValue },
    { k: a.then, v: a.thenValue },
  ];

  return (
    <section
      id="automation"
      className={cn(sectionClassName, "border-b pt-0 sm:pt-0", ui.surface, ui.line)}
    >
      <div
        className={cn(
          containerClassName,
          "flex flex-wrap-reverse items-center gap-x-[72px] gap-y-12",
        )}
      >
        <Reveal
          className={cn(
            "flex min-w-0 flex-[1_1_380px] flex-col gap-3 rounded-[18px] border p-5 sm:p-6",
            ui.card,
            ui.lineStrong,
            ui.shadow,
          )}
        >
          <span className="text-[18px] font-semibold">{a.cardTitle}</span>
          {rows.map((r, i) => (
            <div
              key={r.k}
              className={cn(
                "flex items-center gap-3.5 rounded-[12px] border px-4 py-3.5 transition-colors duration-500",
                ui.inset,
                i === active ? "border-[#2563EB]" : ui.line,
              )}
            >
              <span
                className={cn(
                  "w-[60px] flex-none text-[13px] font-bold uppercase tracking-[0.06em]",
                  ui.eyebrow,
                )}
              >
                {r.k}
              </span>
              <span className="min-w-0 flex-1">{r.v}</span>
              {i === active ? (
                <span
                  aria-hidden
                  className="inline-block size-2 flex-none animate-pulse rounded-full bg-[#5B8DEF]"
                />
              ) : null}
            </div>
          ))}
          <span className={cn("text-[13px]", ui.muted)}>{a.footnote}</span>
        </Reveal>

        <Reveal delay={120} className="flex min-w-0 flex-[1_1_400px] flex-col gap-6">
          <SectionIntro label={a.label} title={a.title} body={a.body} />
          <div className="flex flex-col gap-2.5">
            <span
              className={cn(
                "text-[13px] font-semibold uppercase tracking-[0.06em]",
                ui.muted,
              )}
            >
              {a.moreExamples}
            </span>
            {a.examples.map((ex) => (
              <span key={ex} className={cn("rounded-[10px] border px-3.5 py-2.5", ui.line)}>
                {ex}
              </span>
            ))}
          </div>
        </Reveal>
      </div>
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className={cn("text-[13px] font-semibold", ui.muted)}>{label}</span>
      {children}
    </div>
  );
}

function Chips({ items }: { items: string[] }) {
  return (
    <div className="flex flex-wrap gap-2">
      {items.map((item) => (
        <span
          key={item}
          className={cn("rounded-[8px] border px-2.5 py-1.5 text-[13px] font-medium", ui.inset, ui.lineStrong)}
        >
          {item}
        </span>
      ))}
    </div>
  );
}
