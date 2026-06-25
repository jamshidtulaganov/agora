"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { ArrowRight, Check, Plug, Sparkles } from "lucide-react";
import { useAuthStore } from "@agora/core/auth";
import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import {
  ClaudeCodeLogo,
  CodexLogo,
  GeminiCliLogo,
  OpenClawLogo,
  OpenCodeLogo,
} from "./shared";
import { Reveal } from "./reveal";

const ACCENT = "#2563EB";

export function LandingHero() {
  const { t } = useLocale();
  const user = useAuthStore((s) => s.user);

  return (
    <div className="relative min-h-full overflow-hidden bg-[#05060b] text-white">
      <LandingBackdrop />

      <main className="relative z-10">
        <section
          id="product"
          className="mx-auto max-w-[1320px] px-4 pb-16 pt-28 sm:px-6 sm:pt-32 lg:px-8 lg:pb-24 lg:pt-36"
        >
          <div className="mx-auto max-w-[1120px] text-center">
            <Reveal from="none">
              <p className="text-[12px] font-medium uppercase tracking-[0.34em] text-white/40">
                One board <span className="text-white/25">·</span>{" "}
                <span style={{ color: ACCENT }}>Humans + Agents</span>
              </p>
            </Reveal>

            <Reveal delay={80}>
              <h1 className="mt-5 font-[family-name:var(--font-serif)] text-[3.65rem] leading-[0.93] tracking-[-0.038em] text-white drop-shadow-[0_10px_34px_rgba(0,0,0,0.32)] sm:text-[4.85rem] lg:text-[6.4rem]">
                {t.hero.headlineLine1}
                <br />
                <span
                  style={{ color: ACCENT }}
                  className="drop-shadow-[0_8px_40px_rgba(37,99,235,0.35)]"
                >
                  {t.hero.headlineLine2}
                </span>
              </h1>
            </Reveal>

            <Reveal delay={160}>
              <p className="mx-auto mt-7 max-w-[760px] text-[15px] leading-7 text-white/84 sm:text-[17px]">
                {t.hero.subheading}
              </p>
            </Reveal>

            <Reveal delay={240}>
              <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
                <Link
                  href={user ? "/" : "/login"}
                  className="group inline-flex items-center justify-center gap-2 rounded-[12px] px-5 py-3 text-[14px] font-semibold text-white shadow-[0_14px_40px_-12px_rgba(37,99,235,0.7)] transition-transform hover:-translate-y-0.5"
                  style={{ backgroundColor: ACCENT }}
                >
                  {user ? t.header.dashboard : t.hero.cta}
                  <ArrowRight
                    className="size-4 transition-transform group-hover:translate-x-0.5"
                    aria-hidden
                  />
                </Link>
                <Link
                  href="#preview"
                  className="inline-flex items-center justify-center gap-1.5 rounded-[12px] border border-white/14 bg-white/[0.04] px-5 py-3 text-[14px] font-semibold text-white/85 transition-colors hover:bg-white/[0.08] hover:text-white"
                >
                  See the board
                </Link>
              </div>
            </Reveal>
          </div>

          <Reveal delay={320}>
            <div className="mt-10 flex flex-wrap items-center justify-center gap-x-6 gap-y-3">
              <span className="text-[12px] font-medium uppercase tracking-[0.2em] text-white/35">
                {t.hero.worksWith}
              </span>
              <div className="flex flex-wrap items-center justify-center gap-x-5 gap-y-3">
                {[
                  {
                    icon: <ClaudeCodeLogo className="size-5" />,
                    label: "Claude Code",
                  },
                  { icon: <CodexLogo className="size-5" />, label: "Codex" },
                  {
                    icon: <GeminiCliLogo className="size-5" />,
                    label: "Gemini CLI",
                  },
                  {
                    icon: <OpenClawLogo className="size-5" />,
                    label: "OpenClaw",
                  },
                  {
                    icon: <OpenCodeLogo className="size-5" />,
                    label: "OpenCode",
                  },
                ].map((item, i) => (
                  <Reveal key={item.label} delay={360 + i * 70}>
                    <WorksWithItem icon={item.icon} label={item.label} />
                  </Reveal>
                ))}
              </div>
            </div>
          </Reveal>

          <Reveal delay={380}>
            <div className="mt-6 flex flex-wrap items-center justify-center gap-2.5">
              {[
                { icon: <Sparkles className="size-3.5" />, label: "Agents" },
                { icon: <Sparkles className="size-3.5" />, label: "Skills" },
                { icon: <Plug className="size-3.5" />, label: "MCP" },
              ].map((c) => (
                <span
                  key={c.label}
                  className="inline-flex items-center gap-1.5 rounded-full border border-white/12 bg-white/[0.04] px-3 py-1.5 text-[12px] font-medium text-white/75"
                >
                  <span style={{ color: ACCENT }}>{c.icon}</span>
                  {c.label}
                </span>
              ))}
            </div>
          </Reveal>

          <Reveal
            delay={120}
            className="mx-auto mt-12 max-w-[1180px] scroll-mt-24"
          >
            <div id="preview">
              <BoardMockup />
              <p className="mt-5 text-center text-[12px] uppercase tracking-[0.28em] text-white/30">
                one agent walks it Todo → Done — you just review
              </p>
            </div>
          </Reveal>
        </section>
      </main>
    </div>
  );
}

function WorksWithItem({
  icon,
  label,
}: {
  icon: React.ReactNode;
  label: string;
}) {
  return (
    <div className="flex items-center gap-2.5 text-white/80">
      {icon}
      <span className="text-[15px] font-medium">{label}</span>
    </div>
  );
}

function LandingBackdrop() {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0">
      {/* accent glow */}
      <div
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(120% 80% at 50% -10%, rgba(37,99,235,0.30), transparent 55%)," +
            "radial-gradient(90% 60% at 85% 110%, rgba(37,99,235,0.10), transparent 60%)",
        }}
      />
      {/* faint dotted grid */}
      <div
        className="absolute inset-0 opacity-[0.5]"
        style={{
          backgroundImage:
            "radial-gradient(rgba(255,255,255,0.05) 1px, transparent 1px)",
          backgroundSize: "26px 26px",
          maskImage:
            "radial-gradient(80% 55% at 50% 0%, black, transparent 75%)",
          WebkitMaskImage:
            "radial-gradient(80% 55% at 50% 0%, black, transparent 75%)",
        }}
      />
      {/* center vertical beam */}
      <div
        className="absolute left-1/2 top-0 h-[420px] w-px -translate-x-1/2"
        style={{
          background:
            "linear-gradient(to bottom, rgba(37,99,235,0.55), transparent)",
        }}
      />
    </div>
  );
}

type Assignee = { name: string; initials: string; kind: "agent" | "human" };
type Card = {
  key: string;
  title: string;
  assignee: Assignee;
};

const ARIA: Assignee = { name: "Aria", initials: "AR", kind: "agent" };
const VEGA: Assignee = { name: "Vega", initials: "VE", kind: "agent" };
const JT: Assignee = { name: "JT", initials: "JT", kind: "human" };
const DV: Assignee = { name: "Dilnoza", initials: "DV", kind: "human" };

// Static cards per column. The "live" card (AGORA-128) is NOT here — an agent
// walks it across the columns on a loop (see BoardMockup).
const COLUMNS: { name: string; dot: string; cards: Card[] }[] = [
  {
    name: "Todo",
    dot: "bg-white/45",
    cards: [
      { key: "AGORA-142", title: "Stripe billing webhooks", assignee: JT },
    ],
  },
  {
    name: "In progress",
    dot: "bg-amber-400",
    cards: [
      { key: "AGORA-119", title: "Rebuild P&L cache service", assignee: ARIA },
      { key: "AGORA-117", title: "Role-based access control", assignee: DV },
    ],
  },
  {
    name: "In review",
    dot: "bg-blue-400",
    cards: [
      {
        key: "AGORA-103",
        title: "Export reports to CSV & PDF",
        assignee: VEGA,
      },
    ],
  },
  {
    name: "Done",
    dot: "bg-emerald-400",
    cards: [
      { key: "AGORA-98", title: "Dashboard date filter", assignee: ARIA },
      { key: "AGORA-91", title: "Webhook retry + backoff", assignee: JT },
    ],
  },
];

// The agent-driven card and its per-stage state, indexed by column.
const LIVE_CARD = {
  key: "AGORA-128",
  title: "Persian localization — RTL polish",
};
const STAGE_STATUS = [
  { label: "queued", tone: "text-white/45" },
  { label: "working", tone: "text-amber-300", pulse: true },
  { label: "in review", tone: "text-blue-300" },
  { label: "shipped", tone: "text-emerald-300", done: true },
] as const;

function BoardMockup() {
  const stage = useBoardStage();

  return (
    <div className="overflow-hidden rounded-2xl border border-white/10 bg-[#0a0c16]/80 shadow-[0_40px_120px_-30px_rgba(37,99,235,0.45)]">
      <div className="grid grid-cols-2 gap-3 p-3 sm:p-4 lg:grid-cols-4">
        {COLUMNS.map((col, ci) => {
          const liveHere = stage === ci;
          return (
            <Reveal
              key={col.name}
              delay={ci * 110}
              className="rounded-xl bg-white/[0.03] p-2.5"
            >
              <div className="mb-2 flex items-center justify-between px-1">
                <span className="flex items-center gap-2 text-[12px] font-medium text-white/65">
                  <span
                    className={cn("size-1.5 rounded-full", col.dot)}
                    aria-hidden
                  />
                  {col.name}
                </span>
                <span className="text-[12px] text-white/35">
                  {col.cards.length + (liveHere ? 1 : 0)}
                </span>
              </div>
              <div className="space-y-2">
                {liveHere ? <LiveCard stage={stage} /> : null}
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
            </Reveal>
          );
        })}
      </div>
    </div>
  );
}

/**
 * Drives the live card's column 0→3 on a loop — the "video" of an agent moving
 * an issue Todo → In progress → In review → Done. Parked at Done for
 * reduced-motion.
 */
function useBoardStage() {
  const [stage, setStage] = useState(0);
  useEffect(() => {
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) {
      setStage(STAGE_STATUS.length - 1);
      return;
    }
    const id = setInterval(() => {
      setStage((s) => (s + 1) % STAGE_STATUS.length);
    }, 1600);
    return () => clearInterval(id);
  }, []);
  return stage;
}

function LiveCard({ stage }: { stage: number }) {
  const status = STAGE_STATUS[stage]!;
  return (
    <article
      // Re-mount on every stage so the entrance animation re-runs — reads as
      // the card "landing" in the new column.
      key={stage}
      className="animate-in fade-in slide-in-from-top-2 rounded-lg border border-[#2563EB]/60 bg-[#2563EB]/[0.08] p-2.5 text-left shadow-[0_0_0_1px_rgba(37,99,235,0.25)] duration-500"
    >
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[11px] font-medium tracking-wide text-white/40">
          {LIVE_CARD.key}
        </span>
        <span
          className={cn("flex items-center gap-1 text-[10px] font-medium", status.tone)}
        >
          {"pulse" in status && status.pulse ? (
            <span className="size-1.5 animate-pulse rounded-full bg-amber-400" />
          ) : null}
          {"done" in status && status.done ? <Check className="size-3" /> : null}
          {status.label}
        </span>
      </div>
      <div className="mb-2.5 text-[13px] leading-snug text-white/90">
        {LIVE_CARD.title}
      </div>
      <AssigneeChip assignee={ARIA} />
    </article>
  );
}

function AssigneeChip({ assignee }: { assignee: Assignee }) {
  const isAgent = assignee.kind === "agent";
  return (
    <span className="inline-flex items-center gap-2 text-[11px] font-medium text-white/55">
      <span
        className={cn(
          "flex size-5 items-center justify-center rounded-full text-[9px] font-semibold",
          isAgent ? "text-white" : "bg-white/10 text-white/80",
        )}
        style={isAgent ? { backgroundColor: ACCENT } : undefined}
        aria-hidden
      >
        {isAgent ? <Sparkles className="size-2.5" /> : assignee.initials}
      </span>
      <span className="text-white/80">{assignee.name}</span>
      <span className="text-white/35">· {isAgent ? "agent" : "you"}</span>
    </span>
  );
}
