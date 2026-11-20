"use client";

import { useState } from "react";
import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import type { LandingDeptKey } from "../i18n/types";
import { Reveal } from "./reveal";
import {
  AgentAvatar,
  CheckGlyph,
  PersonAvatar,
  SectionIntro,
  containerClassName,
  primaryButtonClassName,
  sectionClassName,
  secondaryButtonClassName,
  ui,
} from "./shared";

// Fixed facts per demo task, paired by index with `t.how.tasks`.
const TASK_META: {
  dept: LandingDeptKey;
  person?: { name: string; initials: string };
  doneCount: number;
  review: boolean;
}[] = [
  { dept: "support", doneCount: 3, review: true },
  { dept: "sales", doneCount: 1, review: false },
  { dept: "hr", doneCount: 3, review: false },
  { dept: "finance", person: { name: "Malika R.", initials: "MR" }, doneCount: 2, review: false },
];

export function HowSection() {
  const { t } = useLocale();
  const h = t.how;
  const [selected, setSelected] = useState(0);
  const tasks = h.tasks.map((task, i) => {
    const meta = TASK_META[i] ?? TASK_META[0]!;
    return {
      ...task,
      ...meta,
      deptName: t.depts[meta.dept].name,
      who: meta.person ? meta.person.name : t.depts[meta.dept].agent,
    };
  });
  const cur = tasks[selected] ?? tasks[0]!;

  return (
    <section id="how" className={sectionClassName}>
      <div className={cn(containerClassName, "flex flex-col gap-10")}>
        <Reveal>
          <SectionIntro label={h.label} title={h.title} body={h.subtitle} />
        </Reveal>

        <Reveal
          delay={100}
          className={cn("flex flex-wrap overflow-hidden rounded-[18px] border", ui.surface, ui.line)}
        >
          <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-2.5 p-4 sm:p-5">
            <p className={cn("m-0 mb-0.5 text-[13px] font-semibold", ui.muted)}>
              {h.tasksThisWeek}
            </p>
            {tasks.map((task, i) => (
              <button
                key={task.title}
                type="button"
                aria-pressed={i === selected}
                onClick={() => setSelected(i)}
                className={cn(
                  "flex min-h-16 w-full cursor-pointer items-center gap-3 rounded-[12px] border px-3.5 py-3 text-left transition-colors",
                  i === selected ? cn("border-[#2563EB]", ui.selected) : cn(ui.card, ui.line),
                )}
              >
                {task.person ? (
                  <PersonAvatar initials={task.person.initials} size="md" />
                ) : (
                  <AgentAvatar size="md" />
                )}
                <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <span className="text-[15px] font-semibold leading-[1.3]">{task.title}</span>
                  <span className={cn("text-[13px]", ui.muted)}>
                    {task.deptName} · {task.who}
                  </span>
                </span>
              </button>
            ))}
          </div>

          <div
            className={cn(
              "flex min-w-0 flex-[1.2_1_380px] flex-col gap-4 border-t p-5 sm:p-6 md:border-l md:border-t-0",
              ui.card,
              ui.line,
            )}
          >
            <div className="flex flex-wrap items-center gap-2">
              <span
                className={cn(
                  "rounded-full px-2.5 py-1 text-[12px] font-semibold",
                  ui.accentSoft,
                  ui.eyebrow,
                )}
              >
                {cur.status}
              </span>
              <span className={cn("text-[13px]", ui.muted)}>
                {cur.deptName} · {cur.who}
              </span>
            </div>
            <h3 className="m-0 text-[20px] font-semibold leading-[1.3]">{cur.title}</h3>
            <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
              {cur.steps.map((step, i) => (
                <li key={step} className="flex items-start gap-2.5 text-[15px]">
                  {i < cur.doneCount ? (
                    <span
                      role="img"
                      aria-label={h.stepDone}
                      className="flex size-[22px] flex-none items-center justify-center rounded-full bg-[#34C98A] text-[#05060B]"
                    >
                      <CheckGlyph className="size-[13px]" />
                    </span>
                  ) : (
                    <span
                      role="img"
                      aria-label={h.stepOpen}
                      className="mx-0.5 inline-block size-[18px] flex-none rounded-full border-2 border-[#7C879B]"
                    />
                  )}
                  <span>{step}</span>
                </li>
              ))}
            </ul>
            <div className={cn("flex flex-col gap-1.5 rounded-[12px] border px-4 py-3.5", ui.inset, ui.line)}>
              <span
                className={cn(
                  "text-[12px] font-semibold uppercase tracking-[0.06em]",
                  ui.muted,
                )}
              >
                {h.proof}
              </span>
              <span className="text-[15px] font-semibold">{cur.proof}</span>
              <span className={cn("text-[13px]", ui.muted)}>
                {h.source}: {cur.source}
              </span>
            </div>
            {cur.review ? (
              <div className="flex flex-wrap gap-2.5">
                <span className={primaryButtonClassName("md")}>
                  {h.approve}
                </span>
                <span className={secondaryButtonClassName("md")}>
                  {h.askForChanges}
                </span>
              </div>
            ) : null}
          </div>
        </Reveal>
      </div>
    </section>
  );
}
