"use client";

import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { useAuthStore } from "@agora/core/auth";
import { useLocale } from "../i18n";
import {
  ClaudeCodeLogo,
  CodexLogo,
  GeminiCliLogo,
  OpenClawLogo,
  OpenCodeLogo,
  heroButtonClassName,
} from "./shared";

export function LandingHero() {
  const { t } = useLocale();
  const user = useAuthStore((s) => s.user);

  return (
    <div className="relative min-h-full overflow-hidden bg-[#05070b] text-white">
      <LandingBackdrop />

      <main className="relative z-10">
        <section
          id="product"
          className="mx-auto max-w-[1320px] px-4 pb-16 pt-28 sm:px-6 sm:pt-32 lg:px-8 lg:pb-24 lg:pt-36"
        >
          <div className="mx-auto max-w-[1120px] text-center">
            <h1 className="font-[family-name:var(--font-serif)] text-[3.65rem] leading-[0.93] tracking-[-0.038em] text-white drop-shadow-[0_10px_34px_rgba(0,0,0,0.32)] sm:text-[4.85rem] lg:text-[6.4rem]">
              {t.hero.headlineLine1}
              <br />
              {t.hero.headlineLine2}
            </h1>

            <p className="mx-auto mt-7 max-w-[760px] text-[15px] leading-7 text-white/84 sm:text-[17px]">
              {t.hero.subheading}
            </p>

            <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
              <Link href={user ? "/" : "/login"} className={heroButtonClassName("solid")}>
                {user ? t.header.dashboard : t.hero.cta}
              </Link>
              <Link
                href="#preview"
                className="group inline-flex items-center justify-center gap-1.5 rounded-[12px] px-3 py-3 text-[14px] font-semibold text-white/80 transition-colors hover:text-white"
              >
                See the board
                <ArrowRight
                  className="size-4 transition-transform group-hover:translate-x-0.5"
                  aria-hidden
                />
              </Link>
            </div>
          </div>

          <div className="mt-10 flex flex-wrap items-center justify-center gap-x-6 gap-y-3">
            <span className="text-[15px] text-white/50">{t.hero.worksWith}</span>
            <div className="flex flex-wrap items-center justify-center gap-x-5 gap-y-3">
              <div className="flex items-center gap-2.5 text-white/80">
                <ClaudeCodeLogo className="size-5" />
                <span className="text-[15px] font-medium">Claude Code</span>
              </div>
              <div className="flex items-center gap-2.5 text-white/80">
                <CodexLogo className="size-5" />
                <span className="text-[15px] font-medium">Codex</span>
              </div>
              <div className="flex items-center gap-2.5 text-white/80">
                <GeminiCliLogo className="size-5" />
                <span className="text-[15px] font-medium">Gemini CLI</span>
              </div>
              <div className="flex items-center gap-2.5 text-white/80">
                <OpenClawLogo className="size-5" />
                <span className="text-[15px] font-medium">OpenClaw</span>
              </div>
              <div className="flex items-center gap-2.5 text-white/80">
                <OpenCodeLogo className="size-5" />
                <span className="text-[15px] font-medium">OpenCode</span>
              </div>
            </div>
          </div>

          <div id="preview" className="mx-auto mt-12 max-w-[1180px] scroll-mt-24">
            <BoardMockup />
          </div>
        </section>
      </main>
    </div>
  );
}

function LandingBackdrop() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0"
      style={{
        background:
          "radial-gradient(120% 80% at 50% -10%, rgba(37,99,235,0.28), transparent 55%)," +
          "radial-gradient(90% 60% at 85% 110%, rgba(37,99,235,0.10), transparent 60%)",
      }}
    />
  );
}

type Assignee = { initials: string; kind: "agent" | "human" };
type Card = { key: string; title: string; assignee: Assignee };

const COLUMNS: { name: string; tone: string; cards: Card[] }[] = [
  {
    name: "Todo",
    tone: "text-white/55",
    cards: [
      { key: "SDM-107", title: "Persian localization — RTL polish", assignee: { initials: "DV", kind: "agent" } },
      { key: "SDM-18", title: "Export reports to CSV and PDF", assignee: { initials: "AK", kind: "human" } },
    ],
  },
  {
    name: "In progress",
    tone: "text-amber-300/80",
    cards: [
      { key: "SDM-53", title: "P&L cache rebuild service", assignee: { initials: "DV", kind: "agent" } },
      { key: "SDM-10", title: "Stripe billing webhooks", assignee: { initials: "JT", kind: "human" } },
    ],
  },
  {
    name: "In review",
    tone: "text-blue-300/80",
    cards: [
      { key: "SDM-67", title: "Fix «Общая сумма» alignment", assignee: { initials: "DV", kind: "agent" } },
    ],
  },
  {
    name: "Done",
    tone: "text-emerald-300/80",
    cards: [
      { key: "btx-41261", title: "Dashboard date filter", assignee: { initials: "DV", kind: "agent" } },
      { key: "SDM-8", title: "Role-based access control", assignee: { initials: "AK", kind: "human" } },
    ],
  },
];

function BoardMockup() {
  return (
    <div className="overflow-hidden rounded-2xl border border-white/10 bg-[#0a0e16]/80 shadow-[0_30px_80px_-20px_rgba(0,0,0,0.6)] backdrop-blur">
      <div className="flex items-center justify-between gap-3 border-b border-white/10 px-4 py-3">
        <div className="flex items-center gap-2.5">
          <span className="flex size-5 items-center justify-center rounded-[6px] bg-[#2563EB] text-[11px] font-semibold text-white">
            SD
          </span>
          <span className="text-[14px] font-medium text-white/90">Sales Doctor</span>
          <span className="text-white/30">/</span>
          <span className="text-[14px] text-white/60">Issues</span>
        </div>
        <div className="hidden items-center gap-4 text-[13px] text-white/45 sm:flex">
          <span className="flex items-center gap-2">
            <span className="size-2 rounded-full bg-[#3b82f6]" aria-hidden /> Agent
          </span>
          <span className="flex items-center gap-2">
            <span className="size-2 rounded-full bg-[#f5a524]" aria-hidden /> Human
          </span>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3 p-3 sm:p-4 lg:grid-cols-4">
        {COLUMNS.map((col) => (
          <div key={col.name} className="rounded-xl bg-white/[0.03] p-2.5">
            <div className="mb-2 flex items-center justify-between px-1">
              <span className={`text-[12px] font-medium ${col.tone}`}>{col.name}</span>
              <span className="text-[12px] text-white/35">{col.cards.length}</span>
            </div>
            <div className="space-y-2">
              {col.cards.map((card) => (
                <article
                  key={card.key}
                  className="rounded-lg border border-white/[0.06] bg-white/[0.04] p-2.5 text-left"
                >
                  <div className="mb-2 text-[11px] font-medium tracking-wide text-white/35">
                    {card.key}
                  </div>
                  <div className="mb-2.5 text-[13px] leading-snug text-white/90">
                    {card.title}
                  </div>
                  <AssigneeChip assignee={card.assignee} />
                </article>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function AssigneeChip({ assignee }: { assignee: Assignee }) {
  const isAgent = assignee.kind === "agent";
  const ring = isAgent ? "bg-[#3b82f6]/15 text-[#93c5fd]" : "bg-[#f5a524]/15 text-[#fcd34d]";
  const dot = isAgent ? "bg-[#3b82f6]" : "bg-[#f5a524]";
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium ${ring}`}>
      <span className={`size-1.5 rounded-full ${dot}`} aria-hidden />
      {assignee.initials}
      <span className="text-white/40">· {isAgent ? "agent" : "you"}</span>
    </span>
  );
}
