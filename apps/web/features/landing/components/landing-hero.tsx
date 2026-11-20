"use client";

import Link from "next/link";
import { cn } from "@agora/ui/lib/utils";
import { useLocale, type LandingDict } from "../i18n";
import type { LandingDeptKey } from "../i18n/types";
import {
  AgentAvatar,
  PersonAvatar,
  containerClassName,
  primaryButtonClassName,
  secondaryButtonClassName,
  serifClassName,
  ui,
  useStartHref,
  useTicker,
} from "./shared";

type BoardCard = {
  id: string;
  title: string;
  dept: LandingDeptKey;
  // Agent cards show the department agent; person cards show a name.
  person?: { name: string; initials: string };
  note?: string;
  live?: boolean;
};

// People on the demo board. Names are locale-invariant.
const MALIKA = { name: "Malika R.", initials: "MR" };
const AZIZ = { name: "Aziz T.", initials: "AT" };

function boardColumns(t: LandingDict["hero"]["board"], stage: number) {
  const c = t.cards;
  const columns: { name: string; cards: BoardCard[] }[] = [
    {
      name: t.columns.todo,
      cards: [
        { id: "month-end", title: c.monthEnd, dept: "finance" },
        { id: "hiring", title: c.hiring, dept: "hr", person: AZIZ },
      ],
    },
    {
      name: t.columns.inProgress,
      cards: [
        { id: "invoices", title: c.invoices, dept: "finance", person: MALIKA },
      ],
    },
    {
      name: t.columns.inReview,
      cards: [
        { id: "refunds", title: c.refunds, dept: "support", note: t.needsApproval },
      ],
    },
    {
      name: t.columns.done,
      cards: [
        { id: "onboarding", title: c.onboarding, dept: "hr" },
        { id: "weekly", title: c.weekly, dept: "operations" },
      ],
    },
  ];
  // The live card walks Todo → In progress → In review → Done.
  const live: BoardCard = {
    id: "live",
    title: t.liveTitle,
    dept: "sales",
    note: t.liveNotes[stage],
    live: true,
  };
  columns[stage]?.cards.unshift(live);
  return columns;
}

export function LandingHero() {
  const { t } = useLocale();
  const startHref = useStartHref();
  const tick = useTicker(3400);
  // Start in "In progress" so the first frame already shows motion intent.
  const stage = (tick + 1) % 4;
  const columns = boardColumns(t.hero.board, stage);

  return (
    <section id="top" className="px-4 pb-24 pt-16 sm:px-6 sm:pt-[88px]">
      <div className={cn(containerClassName, "flex flex-col items-center gap-14")}>
        <div className="flex max-w-[880px] flex-col items-center gap-6 text-center">
          <p
            className={cn(
              "m-0 rounded-full px-3.5 py-1.5 text-[14px] font-semibold",
              ui.accentSoft,
              ui.eyebrow,
            )}
          >
            {t.hero.badge}
          </p>
          <h1
            className={cn(
              serifClassName,
              "m-0 text-[clamp(3rem,6.6vw,5.75rem)] leading-none tracking-[-0.01em]",
            )}
          >
            {t.hero.titleLead}{" "}
            <span className={ui.accentText}>{t.hero.titleAccent}</span>
          </h1>
          <p
            className={cn(
              "m-0 max-w-[660px] text-[clamp(1.0625rem,1.5vw,1.25rem)] leading-[1.55]",
              ui.muted,
            )}
          >
            {t.hero.subheading}
          </p>
          <div className="flex flex-wrap justify-center gap-3">
            <Link href={startHref} className={primaryButtonClassName()}>
              {t.hero.cta}
            </Link>
            <Link href="/contact-sales" className={secondaryButtonClassName()}>
              {t.hero.talkToSales}
            </Link>
          </div>
        </div>

        <div
          className={cn(
            "w-full overflow-hidden rounded-[18px] border",
            ui.surface,
            ui.line,
            ui.glow,
          )}
        >
          <div className={cn("flex items-center gap-2.5 border-b px-5 py-3.5", ui.line)}>
            <span className="inline-block size-[18px] rounded-[5px] bg-[#2563EB]" />
            <span className="text-[14px] font-semibold">{t.hero.board.workspace}</span>
            <span className={cn("text-[14px]", ui.muted)}>{t.hero.board.allTasks}</span>
          </div>
          <div className="overflow-x-auto">
            <div className="grid min-w-[920px] grid-cols-4 gap-3.5 p-[18px]">
              {columns.map((col) => (
                <div key={col.name} className="flex flex-col gap-2.5">
                  <div className="flex items-center gap-2 px-1 pb-1">
                    <span className="text-[13px] font-semibold">{col.name}</span>
                    <span className={cn("text-[13px]", ui.muted)}>{col.cards.length}</span>
                  </div>
                  {col.cards.map((card) => (
                    <BoardCardView key={card.id} card={card} t={t} />
                  ))}
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

function BoardCardView({ card, t }: { card: BoardCard; t: LandingDict }) {
  const dept = t.depts[card.dept];
  return (
    <div
      className={cn(
        "flex flex-col gap-2.5 rounded-[12px] border p-3",
        ui.card,
        card.note ? "border-[#2563EB]" : ui.line,
        card.live && "animate-in fade-in slide-in-from-left-4 duration-700",
      )}
    >
      <span className="text-[14px] font-semibold leading-[1.35]">{card.title}</span>
      <div className="flex items-center gap-2">
        {card.person ? (
          <PersonAvatar initials={card.person.initials} size="xs" />
        ) : (
          <AgentAvatar size="xs" />
        )}
        <span className={cn("min-w-0 flex-1 truncate text-[12px]", ui.muted)}>
          {card.person ? card.person.name : dept.agent}
        </span>
        <span
          className={cn(
            "rounded-full px-2 py-0.5 text-[11px] font-semibold",
            ui.chip,
            ui.soft,
          )}
        >
          {dept.name}
        </span>
      </div>
      {card.note ? (
        <span className={cn("flex items-center gap-1.5 text-[12px] font-semibold", ui.eyebrow)}>
          <span className="inline-block size-1.5 flex-none animate-pulse rounded-full bg-[#5B8DEF]" />
          {card.note}
        </span>
      ) : null}
    </div>
  );
}
