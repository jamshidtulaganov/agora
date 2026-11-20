"use client";

import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import { Reveal } from "./reveal";
import {
  AgentAvatar,
  CheckGlyph,
  PersonAvatar,
  SectionIntro,
  StepNumber,
  containerClassName,
  sectionClassName,
  ui,
} from "./shared";

const PEOPLE = [
  { name: "Malika R.", initials: "MR" },
  { name: "Aziz T.", initials: "AT" },
];

export function WhatSection() {
  const { t } = useLocale();
  const w = t.what;

  return (
    <section id="what" className={cn(sectionClassName, "border-y", ui.surface, ui.line)}>
      <div
        className={cn(
          containerClassName,
          "flex flex-wrap items-center gap-x-[72px] gap-y-12",
        )}
      >
        <Reveal className="flex min-w-0 flex-[1.3_1_440px] flex-col gap-7">
          <SectionIntro label={w.label} title={w.title} />
          <div className="flex flex-col gap-5">
            {w.points.map((p, i) => (
              <div key={p.title} className="flex gap-4">
                <StepNumber n={i + 1} />
                <div className="flex flex-col gap-1">
                  <h3 className="m-0 text-[18px] font-semibold">{p.title}</h3>
                  <p className={cn("m-0", ui.muted)}>{p.body}</p>
                </div>
              </div>
            ))}
          </div>
        </Reveal>

        <Reveal
          delay={120}
          className={cn(
            "flex min-w-0 max-w-[420px] flex-[1_1_300px] flex-col gap-0.5 rounded-[16px] border p-2.5",
            ui.card,
            ui.lineStrong,
            ui.shadow,
          )}
        >
          <span className={cn("mb-1.5 border-b px-3 py-2.5 text-[14px]", ui.line, ui.muted)}>
            {w.picker.assignTo}
          </span>
          <GroupLabel>{w.picker.people}</GroupLabel>
          {PEOPLE.map((p) => (
            <div key={p.name} className="flex items-center gap-2.5 rounded-[10px] px-3 py-2">
              <PersonAvatar initials={p.initials} />
              <span className="text-[15px]">{p.name}</span>
            </div>
          ))}
          <GroupLabel>{w.picker.agents}</GroupLabel>
          <div
            className={cn(
              "flex items-center gap-2.5 rounded-[10px] border border-[#2563EB] px-3 py-2",
              ui.selected,
            )}
          >
            <AgentAvatar />
            <span className="flex-1 text-[15px] font-semibold">{t.depts.support.agent}</span>
            <span role="img" aria-label={w.picker.selected} className={ui.eyebrow}>
              <CheckGlyph className="size-4" />
            </span>
          </div>
          <div className="flex items-center gap-2.5 rounded-[10px] px-3 py-2">
            <AgentAvatar />
            <span className="text-[15px]">{t.depts.sales.agent}</span>
          </div>
        </Reveal>
      </div>
    </section>
  );
}

function GroupLabel({ children }: { children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "px-3 pb-1.5 pt-2.5 text-[12px] font-semibold uppercase tracking-[0.06em]",
        ui.muted,
      )}
    >
      {children}
    </span>
  );
}
