"use client";

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  Check,
  GitBranch,
  GitMerge,
  GitPullRequest,
  Globe,
  Sparkles,
  X,
} from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import { ClaudeCodeLogo, CodexLogo, GeminiCliLogo } from "./shared";

const ACCENT = "#2563EB";

/**
 * Autoplaying "GIF" tour of Agora, shown in a modal from the hero's
 * "See the board" CTA. No video file — a step counter walks a timeline of
 * sub-steps across four scenes (assign → work → QA → ship) and loops forever.
 * Reduced-motion parks on the final frame.
 */

// Dwell (ms) per sub-step before advancing. Scenes own contiguous ranges.
const TIMELINE = [
  900, // 0  assign: issue panel in
  1000, // 1  assign: picker opens
  900, // 2  assign: agent row selected
  1100, // 3  assign: assigned + queued
  900, // 4  engines: three engine lanes in
  1200, // 5  engines: all running in parallel
  1200, // 6  engines: all done
  900, // 7  work: user message in
  1000, // 8  work: agent replies
  800, // 9  work: Read
  900, // 10 work: Edit + diff lands
  900, // 11 work: Bash
  1200, // 12 work: tests passed
  1000, // 13 qa: preview page renders
  900, // 14 qa: first checks tick
  900, // 15 qa: all checks tick
  1200, // 16 qa: gate passed
  1000, // 17 ship: PR panel in
  1000, // 18 ship: checks green
  1100, // 19 ship: merged, card lands in Done
  2200, // 20 ship: hold, loop
];

// First sub-step of each scene, aligned with t.hero.tour.scenes order.
const SCENE_STARTS = [0, 4, 7, 13, 17];

function sceneForStep(step: number): number {
  let scene = 0;
  for (let i = 0; i < SCENE_STARTS.length; i++) {
    if (step >= SCENE_STARTS[i]!) scene = i;
  }
  return scene;
}

function useTourStep(active: boolean) {
  const [step, setStep] = useState(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!active) return;
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) {
      setStep(TIMELINE.length - 1);
      return;
    }
    setStep(0);
    let current = 0;
    const tick = () => {
      timer.current = setTimeout(() => {
        current = (current + 1) % TIMELINE.length;
        setStep(current);
        tick();
      }, TIMELINE[current]);
    };
    tick();
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [active]);

  return step;
}

export function LandingTourModal({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useLocale();
  const step = useTourStep(open);
  const closeRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    if (!open) return;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prevOverflow;
    };
  }, [open, onClose]);

  if (!open) return null;

  const tour = t.hero.tour;
  const scene = sceneForStep(step);
  const sceneLen =
    (SCENE_STARTS[scene + 1] ?? TIMELINE.length) - SCENE_STARTS[scene]!;
  const sceneSub = step - SCENE_STARTS[scene]!;

  return createPortal(
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center p-4 sm:p-6"
      role="dialog"
      aria-modal="true"
      aria-label={tour.title}
    >
      {/* Backdrop */}
      <button
        type="button"
        aria-label={tour.close}
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-black/60 backdrop-blur-sm"
      />

      <div className="animate-in fade-in zoom-in-95 relative flex max-h-[92vh] w-full max-w-[1000px] flex-col overflow-hidden rounded-2xl border border-zinc-200 bg-white text-[#18181B] shadow-[0_40px_120px_-30px_rgba(37,99,235,0.45)] duration-300 dark:border-white/10 dark:bg-[#0a0c16] dark:text-white">
        {/* Window chrome */}
        <div className="flex items-center gap-2 border-b border-zinc-200 px-4 py-2.5 dark:border-white/8">
          <span className="size-2.5 rounded-full bg-zinc-200 dark:bg-white/15" />
          <span className="size-2.5 rounded-full bg-zinc-200 dark:bg-white/15" />
          <span className="size-2.5 rounded-full bg-zinc-200 dark:bg-white/15" />
          <span className="ml-3 text-[12px] text-[#71717A] dark:text-white/40">
            {tour.title}
          </span>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label={tour.close}
            className="ml-auto grid size-7 place-items-center rounded-md text-[#71717A] transition-colors hover:bg-zinc-100 hover:text-[#18181B] dark:text-white/50 dark:hover:bg-white/10 dark:hover:text-white"
          >
            <X className="size-4" />
          </button>
        </div>

        {/* Story-style scene progress */}
        <div className="flex gap-1.5 px-5 pt-3">
          {tour.scenes.map((s, i) => (
            <div key={s.label} className="flex-1">
              <div className="h-1 overflow-hidden rounded-full bg-zinc-200 dark:bg-white/10">
                <div
                  className="h-full rounded-full transition-all duration-500"
                  style={{
                    backgroundColor: ACCENT,
                    width:
                      i < scene
                        ? "100%"
                        : i === scene
                          ? `${((sceneSub + 1) / sceneLen) * 100}%`
                          : "0%",
                  }}
                />
              </div>
              <div
                className={cn(
                  "mt-1.5 text-[10px] font-semibold uppercase tracking-[0.14em] transition-colors",
                  i === scene
                    ? "text-[#18181B] dark:text-white"
                    : "text-[#A1A1AA] dark:text-white/30",
                )}
              >
                {s.label}
              </div>
            </div>
          ))}
        </div>

        {/* Scene stage — re-keyed so each scene fades in like a GIF frame */}
        <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-2 pt-3 sm:px-5">
          <div
            key={scene}
            className="animate-in fade-in flex min-h-[420px] items-center justify-center rounded-xl border border-zinc-200 bg-zinc-50 p-4 duration-300 dark:border-white/[0.06] dark:bg-white/[0.02] sm:min-h-[460px] sm:p-6"
          >
            {scene === 0 ? <SceneAssign sub={sceneSub} /> : null}
            {scene === 1 ? <SceneEngines sub={sceneSub} /> : null}
            {scene === 2 ? <SceneWork sub={sceneSub} /> : null}
            {scene === 3 ? <SceneQa sub={sceneSub} /> : null}
            {scene === 4 ? <SceneShip sub={sceneSub} /> : null}
          </div>
        </div>

        {/* Caption */}
        <div className="px-5 pb-5 pt-2 text-center">
          <p
            key={scene}
            className="animate-in fade-in slide-in-from-bottom-1 mx-auto max-w-[620px] text-[13px] leading-6 text-[#3F3F46] duration-300 dark:text-white/75 sm:text-[14px]"
          >
            {tour.scenes[scene]?.caption}
          </p>
        </div>
      </div>
    </div>,
    document.body,
  );
}

/* ---------- Scene 1: assign to an agent ---------- */

function SceneAssign({ sub }: { sub: number }) {
  const { t } = useLocale();
  const b = t.hero.board;
  const d = t.demo;
  const mock = t.features.mock;
  const assigned = sub >= 3;

  return (
    <div className="grid w-full max-w-[820px] gap-3 sm:grid-cols-[1fr_260px]">
      {/* Issue detail panel */}
      <div className="animate-in fade-in slide-in-from-bottom-2 rounded-xl border border-zinc-200 bg-white p-4 duration-500 dark:border-white/[0.08] dark:bg-white/[0.04] sm:p-5">
        <div className="flex items-center justify-between">
          <span className="text-[11px] font-medium tracking-wide text-[#A1A1AA] dark:text-white/35">
            AGORA-128
          </span>
          <span
            className={cn(
              "rounded-full px-2 py-0.5 text-[10px] font-medium transition-colors",
              assigned
                ? "bg-[#2563EB]/10 text-[#2563EB] dark:text-blue-300"
                : "bg-zinc-100 text-[#71717A] dark:bg-white/10 dark:text-white/50",
            )}
          >
            {assigned ? b.queued : b.todo}
          </span>
        </div>
        <div className="mt-2 text-[16px] font-medium leading-snug text-[#27272A] dark:text-white/90">
          {b.liveTitle}
        </div>
        <p className="mt-2 text-[13px] leading-6 text-[#71717A] dark:text-white/50">
          {d.userMsg}
        </p>
        {/* Description skeleton */}
        <div className="mt-4 space-y-2">
          <div className="h-2 w-4/5 rounded bg-zinc-100 dark:bg-white/[0.06]" />
          <div className="h-2 w-3/5 rounded bg-zinc-100 dark:bg-white/[0.06]" />
        </div>
        {/* Activity */}
        <div className="mt-5 border-t border-zinc-100 pt-3 dark:border-white/[0.06]">
          <div className="text-[10px] font-semibold uppercase tracking-wide text-[#A1A1AA] dark:text-white/30">
            {mock.activity}
          </div>
          <div
            className={cn(
              "mt-2 flex items-center gap-2 text-[12px] text-[#71717A] transition-all duration-300 dark:text-white/50",
              assigned ? "translate-y-0 opacity-100" : "translate-y-1 opacity-0",
            )}
          >
            <AgentAvatar size={16} />
            <span>
              Aria · {b.queued}
              <span className="ml-1 text-[#A1A1AA] dark:text-white/30">
                · {b.agent}
              </span>
            </span>
          </div>
        </div>
      </div>

      {/* Properties sidebar + assignee picker */}
      <div className="space-y-3">
        <div className="rounded-xl border border-zinc-200 bg-white p-4 text-[12px] dark:border-white/[0.08] dark:bg-white/[0.04]">
          <div className="mb-3 text-[10px] font-semibold uppercase tracking-wide text-[#A1A1AA] dark:text-white/30">
            {mock.properties}
          </div>
          <div className="space-y-2.5">
            <PropRow label={d.statusLabel}>
              <span
                className={cn(
                  "rounded px-1.5 py-0.5 transition-colors",
                  assigned
                    ? "bg-[#2563EB]/10 text-[#2563EB] dark:text-blue-300"
                    : "bg-zinc-100 text-[#71717A] dark:bg-white/10 dark:text-white/50",
                )}
              >
                {assigned ? b.queued : d.statusTodo}
              </span>
            </PropRow>
            <PropRow label={d.priorityLabel}>
              <span className="text-[#52525B] dark:text-white/70">
                {d.priorityHigh}
              </span>
            </PropRow>
            <PropRow label={d.projectLabel}>
              <span className="text-[#52525B] dark:text-white/70">
                {d.project}
              </span>
            </PropRow>
            <PropRow label={d.assigneeLabel}>
              {assigned ? (
                <span className="animate-in fade-in flex items-center gap-1.5 text-[#27272A] duration-300 dark:text-white/85">
                  <AgentAvatar size={16} /> Aria
                </span>
              ) : (
                <span className="rounded border border-dashed border-zinc-300 px-1.5 py-0.5 text-[11px] text-[#A1A1AA] dark:border-white/15 dark:text-white/35">
                  {mock.unassigned}
                </span>
              )}
            </PropRow>
          </div>
        </div>

        {/* Assignee picker drops in, agent row gets picked */}
        {sub >= 1 && !assigned ? (
          <div className="animate-in fade-in slide-in-from-top-1 rounded-xl border border-zinc-200 bg-white p-1.5 shadow-lg duration-300 dark:border-white/10 dark:bg-[#10131f]">
            <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-[#A1A1AA] dark:text-white/30">
              {mock.members}
            </div>
            <PickerRow initials="JT" name="JT" />
            <PickerRow initials="DV" name="Dilnoza" />
            <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-[#A1A1AA] dark:text-white/30">
              {mock.agents}
            </div>
            <PickerRow agent name="Aria" engine="Claude Code" selected={sub >= 2} />
            <PickerRow agent name="Vega" engine="Codex" />
            <PickerRow agent name="Nova" engine="Gemini CLI" />
          </div>
        ) : null}
      </div>
    </div>
  );
}

function PropRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="w-16 shrink-0 text-[#71717A] dark:text-white/40">
        {label}
      </span>
      {children}
    </div>
  );
}

function PickerRow({
  name,
  initials,
  engine,
  agent = false,
  selected = false,
}: {
  name: string;
  initials?: string;
  engine?: string;
  agent?: boolean;
  selected?: boolean;
}) {
  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-lg px-2 py-1.5 text-[12px] transition-colors",
        selected
          ? "bg-[#2563EB]/10 text-[#18181B] dark:bg-[#2563EB]/20 dark:text-white"
          : "text-[#3F3F46] dark:text-white/70",
      )}
    >
      {agent ? (
        <AgentAvatar size={18} />
      ) : (
        <span className="grid size-[18px] place-items-center rounded-full bg-zinc-200 text-[8px] font-semibold text-[#52525B] dark:bg-white/10 dark:text-white/80">
          {initials}
        </span>
      )}
      {name}
      <span className="ml-auto flex items-center gap-1.5">
        {engine ? (
          <span className="text-[10px] text-[#A1A1AA] dark:text-white/35">
            {engine}
          </span>
        ) : null}
        {selected ? (
          <Check className="size-3.5" style={{ color: ACCENT }} />
        ) : null}
      </span>
    </div>
  );
}

/* ---------- Scene 2: one task, any LLM engine ---------- */

const ENGINE_LANES = [
  { engine: "Claude Code", agent: "Aria", Logo: ClaudeCodeLogo, grow: "62%" },
  { engine: "Codex", agent: "Vega", Logo: CodexLogo, grow: "48%" },
  { engine: "Gemini CLI", agent: "Nova", Logo: GeminiCliLogo, grow: "71%" },
];

function SceneEngines({ sub }: { sub: number }) {
  const { t } = useLocale();
  const b = t.hero.board;
  const d = t.demo;
  const done = sub >= 2;

  return (
    <div className="w-full max-w-[860px]">
      {/* The one task all engines share */}
      <div className="mx-auto mb-4 flex max-w-[420px] items-center gap-2 rounded-lg border border-zinc-200 bg-white px-3 py-2 dark:border-white/[0.08] dark:bg-white/[0.04]">
        <span className="text-[11px] font-medium tracking-wide text-[#A1A1AA] dark:text-white/35">
          AGORA-128
        </span>
        <span className="truncate text-[13px] text-[#27272A] dark:text-white/90">
          {b.liveTitle}
        </span>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        {ENGINE_LANES.map((lane, i) => {
          const running = sub >= 1 && !done;
          return (
            <div
              key={lane.engine}
              className="animate-in fade-in slide-in-from-bottom-2 rounded-xl border border-zinc-200 bg-white p-4 duration-500 dark:border-white/[0.08] dark:bg-white/[0.04]"
              style={{ animationDelay: `${i * 120}ms`, animationFillMode: "backwards" }}
            >
              <div className="flex items-center gap-2.5">
                <lane.Logo className="size-6 shrink-0" />
                <div className="min-w-0">
                  <div className="truncate text-[13px] font-medium text-[#27272A] dark:text-white/90">
                    {lane.engine}
                  </div>
                  <div className="flex items-center gap-1.5 text-[11px] text-[#71717A] dark:text-white/45">
                    <AgentAvatar size={14} /> {lane.agent}
                  </div>
                </div>
              </div>

              {/* Progress */}
              <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-zinc-100 dark:bg-white/[0.08]">
                <div
                  className={cn(
                    "h-full rounded-full transition-all duration-700",
                    done && "bg-emerald-500",
                  )}
                  style={{
                    backgroundColor: done ? undefined : ACCENT,
                    width: done ? "100%" : sub >= 1 ? lane.grow : "6%",
                  }}
                />
              </div>

              <div className="mt-2.5 flex items-center gap-1.5 text-[11px]">
                {done ? (
                  <span className="flex items-center gap-1 text-emerald-600 dark:text-emerald-300">
                    <Check className="size-3" /> {d.testsPassed}
                  </span>
                ) : running ? (
                  <span className="flex items-center gap-1.5 text-[#71717A] dark:text-white/45">
                    <span
                      className="size-1.5 animate-pulse rounded-full"
                      style={{ backgroundColor: ACCENT }}
                    />
                    {b.working}
                  </span>
                ) : (
                  <span className="text-[#A1A1AA] dark:text-white/35">
                    {b.queued}
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/* ---------- Scene 2: agent works the issue ---------- */

const TOUR_TOOLS = [
  { tool: "Read", text: "dashboard/rtl-layout.tsx" },
  { tool: "Edit", text: "mirror chevrons, flip grid columns" },
  { tool: "Bash", text: "pnpm test rtl" },
];

// Diff rows read as code artifacts — untranslated, like the tool args.
const TOUR_DIFF: { kind: "ctx" | "del" | "add"; text: string }[] = [
  { kind: "ctx", text: "export function DashboardShell() {" },
  { kind: "del", text: '  <aside className="left-0 border-r">' },
  { kind: "add", text: '  <aside className="start-0 border-e">' },
  { kind: "del", text: '  grid-cols-[240px_1fr]' },
  { kind: "add", text: '  grid-cols-[240px_1fr] rtl:grid-cols-[1fr_240px]' },
  { kind: "ctx", text: "  <ChevronStart aria-hidden />" },
];

function SceneWork({ sub }: { sub: number }) {
  const { t } = useLocale();
  const d = t.demo;

  return (
    <div className="grid w-full max-w-[860px] gap-3 sm:grid-cols-2">
      {/* Conversation + tool stream */}
      <div className="space-y-3">
        <Bubble side="right" show={sub >= 0}>
          <span className="text-[#27272A] dark:text-white/90">
            <span style={{ color: ACCENT }}>@Aria</span> {d.userMsg}
          </span>
        </Bubble>
        {sub >= 1 ? (
          <Bubble side="left" agent show>
            <span className="text-[#3F3F46] dark:text-white/85">
              {d.agentReply}
            </span>
          </Bubble>
        ) : null}

        {sub >= 2 ? (
          <div className="animate-in fade-in slide-in-from-bottom-1 rounded-xl border border-zinc-200 bg-white p-3 duration-300 dark:border-white/[0.08] dark:bg-white/[0.04]">
            <div className="flex items-center gap-2">
              <AgentAvatar size={20} />
              <span className="text-[12px] font-medium text-[#3F3F46] dark:text-white/85">
                {d.working}
              </span>
              {sub >= 5 ? (
                <span className="ml-auto flex items-center gap-1 text-[11px] text-emerald-600 dark:text-emerald-300">
                  <Check className="size-3" /> {d.done}
                </span>
              ) : (
                <span className="ml-auto flex items-center gap-1.5 text-[11px] text-[#71717A] dark:text-white/45">
                  <span
                    className="size-1.5 animate-pulse rounded-full"
                    style={{ backgroundColor: ACCENT }}
                  />
                  {d.running}
                </span>
              )}
            </div>
            <div className="mt-2.5 space-y-1.5 font-mono text-[12px]">
              {TOUR_TOOLS.map((row, i) => (
                <div
                  key={row.tool}
                  className={cn(
                    "flex items-center gap-2 transition-all duration-300",
                    sub >= 2 + i
                      ? "translate-y-0 text-[#52525B] opacity-100 dark:text-white/55"
                      : "translate-y-1 opacity-0",
                  )}
                >
                  <span className="shrink-0 font-semibold text-[#27272A] dark:text-white/80">
                    {row.tool}
                  </span>
                  <span className="truncate">{row.text}</span>
                </div>
              ))}
              <div
                className={cn(
                  "flex items-center gap-2 transition-all duration-300",
                  sub >= 5
                    ? "translate-y-0 text-emerald-600 opacity-100 dark:text-emerald-300/80"
                    : "translate-y-1 opacity-0",
                )}
              >
                <span className="shrink-0 text-[#A1A1AA] dark:text-white/35">
                  ›
                </span>
                <span className="truncate">{d.testsPassed}</span>
              </div>
            </div>
          </div>
        ) : null}

        <div className="text-center text-[11px] text-amber-600 dark:text-amber-300">
          {sub >= 2 ? d.statusInProgress : " "}
        </div>
      </div>

      {/* Live diff panel */}
      <div className="overflow-hidden rounded-xl border border-zinc-200 bg-white dark:border-white/[0.08] dark:bg-white/[0.04]">
        <div className="flex items-center gap-2 border-b border-zinc-200 px-3 py-2 dark:border-white/8">
          <GitBranch className="size-3.5 text-[#A1A1AA] dark:text-white/35" />
          <span className="font-mono text-[11px] text-[#71717A] dark:text-white/45">
            dashboard/rtl-layout.tsx
          </span>
          <span
            className={cn(
              "ml-auto font-mono text-[11px] transition-opacity duration-300",
              sub >= 3 ? "opacity-100" : "opacity-0",
            )}
          >
            <span className="text-emerald-600 dark:text-emerald-300">+124</span>{" "}
            <span className="text-red-500 dark:text-red-400">−38</span>
          </span>
        </div>
        <div className="space-y-0.5 p-3 font-mono text-[11px] leading-5">
          {TOUR_DIFF.map((row, i) => (
            <div
              key={i}
              className={cn(
                "truncate rounded px-1.5 transition-all duration-300",
                sub >= 3
                  ? "translate-y-0 opacity-100"
                  : "translate-y-1 opacity-0",
                row.kind === "add" &&
                  "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
                row.kind === "del" &&
                  "bg-red-500/10 text-red-600 line-through dark:text-red-400",
                row.kind === "ctx" && "text-[#A1A1AA] dark:text-white/35",
              )}
              style={{ transitionDelay: `${i * 90}ms` }}
            >
              {row.kind === "add" ? "+ " : row.kind === "del" ? "− " : "  "}
              {row.text}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function Bubble({
  side,
  agent = false,
  show,
  children,
}: {
  side: "left" | "right";
  agent?: boolean;
  show: boolean;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex items-end gap-2 transition-all duration-400",
        side === "right" ? "flex-row-reverse" : "flex-row",
        show ? "translate-y-0 opacity-100" : "translate-y-2 opacity-0",
      )}
    >
      {agent ? (
        <AgentAvatar size={22} />
      ) : (
        <span className="grid size-[22px] shrink-0 place-items-center rounded-full bg-zinc-200 text-[9px] font-semibold text-[#52525B] dark:bg-white/10 dark:text-white/80">
          JT
        </span>
      )}
      <div
        className={cn(
          "max-w-[82%] rounded-2xl px-3 py-2 text-[13px] leading-snug",
          side === "right"
            ? "rounded-br-sm bg-zinc-100 dark:bg-white/10"
            : "rounded-bl-sm border border-zinc-200 bg-white dark:border-white/[0.06] dark:bg-white/[0.03]",
        )}
      >
        {children}
      </div>
    </div>
  );
}

/* ---------- Scene 3: QA gate in a real browser ---------- */

// Check names read as code artifacts (like tool args above) — untranslated.
const QA_CHECKS = [
  "login flow",
  "rtl layout",
  "dashboard date filter",
  "csv export",
  "console errors",
];

function qaCheckDone(sub: number, index: number): boolean {
  if (sub >= 2) return true;
  return sub >= 1 && index < 2;
}

function SceneQa({ sub }: { sub: number }) {
  const { t } = useLocale();
  const b = t.hero.board;
  const passed = sub >= 3;

  return (
    <div className="grid w-full max-w-[860px] gap-3 sm:grid-cols-[1.2fr_1fr]">
      {/* Browser preview of the change (RTL dashboard skeleton) */}
      <div className="overflow-hidden rounded-xl border border-zinc-200 bg-white dark:border-white/[0.08] dark:bg-white/[0.04]">
        <div className="flex items-center gap-2 border-b border-zinc-200 px-3 py-2 dark:border-white/8">
          <Globe className="size-3.5 text-[#A1A1AA] dark:text-white/35" />
          <span className="flex-1 truncate rounded-md bg-zinc-100 px-2 py-1 font-mono text-[11px] text-[#71717A] dark:bg-white/[0.06] dark:text-white/45">
            qa · preview.agora.dev/AGORA-128
          </span>
        </div>
        {/* RTL page mock: sidebar on the right, content flows right-to-left */}
        <div
          dir="rtl"
          className={cn(
            "grid min-h-[220px] grid-cols-[72px_1fr] gap-3 p-3 transition-opacity duration-500",
            sub >= 0 ? "opacity-100" : "opacity-0",
          )}
        >
          <div className="space-y-2 rounded-lg bg-zinc-100 p-2 dark:bg-white/[0.05]">
            {[0, 1, 2, 3].map((i) => (
              <div
                key={i}
                className="h-2 rounded bg-zinc-200 dark:bg-white/[0.08]"
              />
            ))}
          </div>
          <div className="space-y-2.5">
            <div className="h-3 w-2/5 rounded bg-zinc-200 dark:bg-white/[0.1]" />
            <div className="grid grid-cols-3 gap-2">
              {[0, 1, 2].map((i) => (
                <div
                  key={i}
                  className="h-12 rounded-lg bg-zinc-100 dark:bg-white/[0.05]"
                />
              ))}
            </div>
            <div className="h-2 w-4/5 rounded bg-zinc-100 dark:bg-white/[0.06]" />
            <div className="h-2 w-3/5 rounded bg-zinc-100 dark:bg-white/[0.06]" />
            <div className="h-2 w-2/3 rounded bg-zinc-100 dark:bg-white/[0.06]" />
          </div>
        </div>
        <div className="border-t border-zinc-200 px-3 py-2 font-mono text-[11px] text-[#A1A1AA] dark:border-white/8 dark:text-white/35">
          {passed ? (
            <span className="text-emerald-600 dark:text-emerald-300">
              › 0 console errors
            </span>
          ) : (
            <span>› …</span>
          )}
        </div>
      </div>

      {/* run_qa gate checklist */}
      <div className="flex flex-col overflow-hidden rounded-xl border border-zinc-200 bg-white dark:border-white/[0.08] dark:bg-white/[0.04]">
        <div className="flex items-center gap-2 border-b border-zinc-200 px-3 py-2 dark:border-white/8">
          <span className="font-mono text-[11px] font-semibold text-[#3F3F46] dark:text-white/70">
            run_qa
          </span>
          {passed ? (
            <span className="ml-auto flex items-center gap-1 text-[11px] font-medium text-emerald-600 dark:text-emerald-300">
              <Check className="size-3" /> {b.review}
            </span>
          ) : (
            <span className="ml-auto flex items-center gap-1.5 text-[11px] text-[#71717A] dark:text-white/45">
              <span
                className="size-1.5 animate-pulse rounded-full"
                style={{ backgroundColor: ACCENT }}
              />
              {b.working}
            </span>
          )}
        </div>
        <div className="flex-1 space-y-2 p-3 font-mono text-[12px]">
          {QA_CHECKS.map((check, i) => {
            const done = qaCheckDone(sub, i);
            return (
              <div key={check} className="flex items-center gap-2">
                {done ? (
                  <Check className="size-3 shrink-0 text-emerald-600 dark:text-emerald-300" />
                ) : (
                  <span
                    className="size-1.5 shrink-0 animate-pulse rounded-full"
                    style={{ backgroundColor: ACCENT }}
                  />
                )}
                <span
                  className={cn(
                    "truncate transition-colors",
                    done
                      ? "text-[#52525B] dark:text-white/55"
                      : "text-[#A1A1AA] dark:text-white/30",
                  )}
                >
                  {check}
                </span>
              </div>
            );
          })}
        </div>
        <div
          className={cn(
            "border-t border-zinc-200 px-3 py-2 text-center text-[11px] font-medium transition-colors dark:border-white/8",
            passed
              ? "text-emerald-600 dark:text-emerald-300"
              : "text-[#A1A1AA] dark:text-white/30",
          )}
        >
          {passed ? `${QA_CHECKS.length}/${QA_CHECKS.length} ✓` : "…"}
        </div>
      </div>
    </div>
  );
}

/* ---------- Scene 4: PR ready, card lands in Done ---------- */

function SceneShip({ sub }: { sub: number }) {
  const { t } = useLocale();
  const b = t.hero.board;
  const d = t.demo;
  const merged = sub >= 2;

  return (
    <div className="grid w-full max-w-[860px] gap-3 sm:grid-cols-[1.1fr_1fr]">
      {/* Pull request panel */}
      <div className="animate-in fade-in slide-in-from-bottom-2 flex flex-col rounded-xl border border-zinc-200 bg-white p-4 duration-500 dark:border-white/[0.08] dark:bg-white/[0.04]">
        <div className="flex items-center gap-2">
          {merged ? (
            <GitMerge className="size-4 text-purple-500 dark:text-purple-300" />
          ) : (
            <GitPullRequest className="size-4" style={{ color: ACCENT }} />
          )}
          <span className="text-[14px] font-medium text-[#27272A] dark:text-white/90">
            {d.pr}
          </span>
          <span
            className={cn(
              "ml-auto rounded-full px-2 py-0.5 text-[10px] font-medium transition-colors",
              merged
                ? "bg-purple-500/10 text-purple-600 dark:text-purple-300"
                : "bg-emerald-500/10 text-emerald-600 dark:text-emerald-300",
            )}
          >
            {merged ? b.shipped : d.readyToMerge}
          </span>
        </div>

        <div className="mt-3 flex items-center gap-1.5 font-mono text-[11px] text-[#71717A] dark:text-white/45">
          <span className="rounded bg-[#2563EB]/10 px-1.5 py-0.5 text-[#2563EB] dark:text-blue-300">
            agora/rtl-polish
          </span>
          <span>→</span>
          <span className="rounded bg-zinc-100 px-1.5 py-0.5 dark:bg-white/[0.08]">
            main
          </span>
          <span className="ml-auto">
            <span className="text-emerald-600 dark:text-emerald-300">+124</span>{" "}
            <span className="text-red-500 dark:text-red-400">−38</span>
          </span>
        </div>

        {/* PR checks */}
        <div className="mt-4 space-y-2 border-t border-zinc-100 pt-3 font-mono text-[12px] dark:border-white/[0.06]">
          {["run_qa gate", "12 tests", "typecheck"].map((check) => (
            <div key={check} className="flex items-center gap-2">
              {sub >= 1 ? (
                <Check className="size-3 shrink-0 text-emerald-600 dark:text-emerald-300" />
              ) : (
                <span
                  className="size-1.5 shrink-0 animate-pulse rounded-full"
                  style={{ backgroundColor: ACCENT }}
                />
              )}
              <span className="text-[#52525B] dark:text-white/55">{check}</span>
            </div>
          ))}
        </div>

        <div className="mt-auto pt-4">
          <div
            className={cn(
              "rounded-lg py-2 text-center text-[12px] font-semibold text-white transition-colors",
              merged ? "bg-purple-500" : "",
            )}
            style={merged ? undefined : { backgroundColor: ACCENT }}
          >
            {merged ? (
              <span className="inline-flex items-center gap-1.5">
                <GitMerge className="size-3.5" /> {b.shipped}
              </span>
            ) : (
              d.readyToMerge
            )}
          </div>
        </div>
      </div>

      {/* Done column receives the live card */}
      <div className="rounded-xl bg-white p-3 dark:bg-white/[0.03]">
        <div className="mb-2 flex items-center justify-between px-1">
          <span className="flex items-center gap-2 text-[12px] font-medium text-[#52525B] dark:text-white/65">
            <span className="size-1.5 rounded-full bg-emerald-400" />
            {b.done}
          </span>
          <span className="text-[12px] text-[#A1A1AA] dark:text-white/35">
            {merged ? 3 : 2}
          </span>
        </div>
        <div className="space-y-2">
          {merged ? (
            <article className="animate-in fade-in slide-in-from-top-2 rounded-lg border border-[#2563EB]/40 bg-[#2563EB]/[0.06] p-2.5 duration-500 dark:border-[#2563EB]/60 dark:bg-[#2563EB]/[0.08]">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-[11px] font-medium tracking-wide text-[#71717A] dark:text-white/40">
                  AGORA-128
                </span>
                <span className="flex items-center gap-1 text-[10px] font-medium text-emerald-600 dark:text-emerald-300">
                  <Check className="size-3" /> {b.shipped}
                </span>
              </div>
              <div className="mb-2 text-[13px] leading-snug text-[#27272A] dark:text-white/90">
                {b.liveTitle}
              </div>
              <span className="inline-flex items-center gap-1.5 text-[11px] text-[#71717A] dark:text-white/55">
                <AgentAvatar size={16} /> Aria
                <span className="text-[#A1A1AA] dark:text-white/35">
                  · {b.agent}
                </span>
              </span>
            </article>
          ) : null}
          <article className="rounded-lg border border-zinc-200 bg-white p-2.5 dark:border-white/[0.06] dark:bg-white/[0.04]">
            <div className="mb-2 text-[11px] font-medium tracking-wide text-[#A1A1AA] dark:text-white/35">
              AGORA-98
            </div>
            <div className="text-[13px] leading-snug text-[#27272A] dark:text-white/90">
              {b.card5}
            </div>
          </article>
          <article className="rounded-lg border border-zinc-200 bg-white p-2.5 dark:border-white/[0.06] dark:bg-white/[0.04]">
            <div className="mb-2 text-[11px] font-medium tracking-wide text-[#A1A1AA] dark:text-white/35">
              AGORA-91
            </div>
            <div className="text-[13px] leading-snug text-[#27272A] dark:text-white/90">
              {b.card6}
            </div>
          </article>
        </div>
      </div>
    </div>
  );
}

function AgentAvatar({ size = 20 }: { size?: number }) {
  return (
    <span
      className="inline-flex shrink-0 items-center justify-center rounded-full text-white"
      style={{ width: size, height: size, backgroundColor: ACCENT }}
    >
      <Sparkles style={{ width: size * 0.5, height: size * 0.5 }} />
    </span>
  );
}
