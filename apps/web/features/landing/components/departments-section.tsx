"use client";

import { useState } from "react";
import Link from "next/link";
import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import type { LandingDeptKey } from "../i18n/types";
import { Reveal } from "./reveal";
import {
  SectionIntro,
  containerClassName,
  eyebrowClassName,
  sectionClassName,
  ui,
} from "./shared";

const ORDER: LandingDeptKey[] = [
  "support",
  "sales",
  "hr",
  "finance",
  "operations",
  "engineering",
];
// "More example tasks" grid reads in this order.
const GRID_ORDER: LandingDeptKey[] = [
  "sales",
  "support",
  "hr",
  "finance",
  "operations",
  "engineering",
];

export function DepartmentsSection() {
  const { t } = useLocale();
  const d = t.departments;
  const [selected, setSelected] = useState<LandingDeptKey>("support");
  const team = d.teams[selected];
  const agent = t.depts[selected].agent;

  return (
    <section id="departments" className={cn(sectionClassName, "border-y", ui.surface, ui.line)}>
      <div className={cn(containerClassName, "flex flex-col gap-12")}>
        <Reveal>
          <SectionIntro label={d.label} title={d.title} body={d.body} />
        </Reveal>

        <Reveal delay={100} className="flex flex-col gap-4">
          <div className="flex flex-wrap gap-2">
            {ORDER.map((key) => (
              <button
                key={key}
                type="button"
                aria-pressed={key === selected}
                onClick={() => setSelected(key)}
                className={cn(
                  "min-h-11 cursor-pointer rounded-full border px-[18px] py-2.5 text-[15px] font-semibold transition-colors",
                  key === selected
                    ? "border-[#2563EB] bg-[#2563EB] text-white"
                    : cn(ui.card, ui.lineStrong),
                )}
              >
                {t.depts[key].name}
              </button>
            ))}
          </div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(240px,1fr))] gap-4">
            <StepCard label={d.youAsk} text={team.ask} strong />
            <StepCard label={d.agentDoes.replace("{agent}", agent)} text={team.does} />
            <StepCard label={d.youGet} text={team.get} strong accent />
          </div>
          <p className={cn("m-0", ui.muted)}>
            {d.setupOnce} {team.setup}{" "}
            <Link href="/usecases" className={cn("font-semibold no-underline", ui.eyebrow)}>
              {d.seeAll}
            </Link>
          </p>
        </Reveal>

        <p className={cn(eyebrowClassName, ui.muted)}>{d.moreTasks}</p>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,300px),1fr))] gap-5">
          {GRID_ORDER.map((key, i) => (
            <Reveal
              key={key}
              delay={i * 60}
              className={cn("flex flex-col gap-3.5 rounded-[16px] border p-6", ui.card, ui.line)}
            >
              <h3 className="m-0 text-[20px] font-semibold">{t.depts[key].name}</h3>
              <ul className={cn("m-0 flex flex-col gap-2 pl-5", ui.muted)}>
                {d.teams[key].tasks.map((task) => (
                  <li key={task}>{task}</li>
                ))}
              </ul>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  );
}

function StepCard({
  label,
  text,
  strong = false,
  accent = false,
}: {
  label: string;
  text: string;
  strong?: boolean;
  accent?: boolean;
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-2.5 rounded-[16px] border p-6",
        ui.card,
        accent ? "border-[#2563EB]" : ui.line,
      )}
    >
      <span
        className={cn(
          "text-[12px] font-bold uppercase tracking-[0.06em]",
          accent ? ui.eyebrow : ui.muted,
        )}
      >
        {label}
      </span>
      <span className={cn("text-[18px] leading-[1.35]", strong ? "font-semibold" : ui.soft)}>
        {text}
      </span>
    </div>
  );
}
