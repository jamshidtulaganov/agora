"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowUp, Check, GitPullRequest, Sparkles } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { Reveal } from "./reveal";

const ACCENT = "#2563EB";

/**
 * Auto-playing product demo: a looping, self-driven "screen recording" (no
 * video file) of a human assigning an issue to an agent, the agent chatting
 * back, streaming its tool calls, and shipping a PR. Drives a step counter on
 * a timer and renders content cumulatively. Pauses on reduced-motion (shows the
 * finished state).
 */

// Each entry is how long (ms) to dwell before advancing to the next step.
const TIMELINE = [
  700, // 0 user message in
  1100, // 1 agent typing
  1400, // 2 agent reply
  900, // 3 working card
  800, // 4 Read
  800, // 5 Edit
  900, // 6 Bash
  1500, // 7 result + done + PR
  1800, // 8 hold, then loop
];

const TOOLS = [
  { tool: "Read", text: "dashboard/rtl-layout.tsx" },
  { tool: "Edit", text: "mirror chevrons, flip grid columns" },
  { tool: "Bash", text: "pnpm test rtl" },
];

export function ProductDemo() {
  const [step, setStep] = useState(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) {
      setStep(TIMELINE.length - 1);
      return;
    }
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
  }, []);

  const status = step >= 7 ? "In review" : step >= 3 ? "In progress" : "Todo";
  const statusTone =
    step >= 7
      ? "text-blue-600 bg-blue-400/15 dark:text-blue-300"
      : step >= 3
        ? "text-amber-600 bg-amber-400/15 dark:text-amber-300"
        : "text-[#71717A] bg-zinc-100 dark:text-white/50 dark:bg-white/10";

  return (
    <section className="relative overflow-hidden bg-white py-24 text-[#18181B] dark:bg-[#05060b] dark:text-white sm:py-32">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 dark:hidden"
        style={{
          background:
            "radial-gradient(70% 50% at 50% 0%, rgba(37,99,235,0.07), transparent 60%)",
        }}
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 hidden dark:block"
        style={{
          background:
            "radial-gradient(70% 50% at 50% 0%, rgba(37,99,235,0.18), transparent 60%)",
        }}
      />
      <div className="relative mx-auto max-w-[980px] px-4 sm:px-6 lg:px-8">
        <Reveal className="text-center">
          <p className="text-[12px] font-medium uppercase tracking-[0.28em] text-[#71717A] dark:text-white/40">
            See it in action
          </p>
          <h2 className="mx-auto mt-4 max-w-[680px] font-[family-name:var(--font-serif)] text-[2.4rem] leading-[1.05] tracking-[-0.03em] sm:text-[3.2rem]">
            Assign an issue. Watch the agent ship it.
          </h2>
        </Reveal>

        <Reveal delay={120} className="mt-12">
          <div className="overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-[0_30px_90px_-40px_rgba(37,99,235,0.30)] dark:border-white/10 dark:bg-[#0a0c16] dark:shadow-[0_40px_120px_-30px_rgba(37,99,235,0.45)]">
            {/* Window chrome */}
            <div className="flex items-center gap-2 border-b border-zinc-200 px-4 py-2.5 dark:border-white/8">
              <span className="size-2.5 rounded-full bg-zinc-200 dark:bg-white/15" />
              <span className="size-2.5 rounded-full bg-zinc-200 dark:bg-white/15" />
              <span className="size-2.5 rounded-full bg-zinc-200 dark:bg-white/15" />
              <span className="ml-3 text-[12px] text-[#71717A] dark:text-white/40">
                AGORA-128 · Persian localization — RTL polish
              </span>
              <span
                className={cn(
                  "ml-auto rounded-full px-2.5 py-0.5 text-[11px] font-medium transition-colors",
                  statusTone,
                )}
              >
                {status}
              </span>
            </div>

            <div className="grid gap-0 sm:grid-cols-[1fr_240px]">
              {/* Conversation */}
              <div className="min-h-[320px] space-y-3 border-b border-zinc-200 p-5 dark:border-white/8 sm:border-b-0 sm:border-r">
                {/* User message */}
                <Bubble side="right" show={step >= 0}>
                  <span className="text-[#27272A] dark:text-white/90">
                    <span style={{ color: ACCENT }}>@Aria</span> the dashboard
                    breaks in right-to-left — can you fix the alignment?
                  </span>
                </Bubble>

                {/* Agent typing / reply */}
                {step >= 1 && step < 2 ? (
                  <Bubble side="left" agent show>
                    <Typing />
                  </Bubble>
                ) : null}
                {step >= 2 ? (
                  <Bubble side="left" agent show>
                    <span className="text-[#3F3F46] dark:text-white/85">
                      On it — auditing the layout files and flipping the grid
                      now.
                    </span>
                  </Bubble>
                ) : null}

                {/* Working card */}
                {step >= 3 ? (
                  <div className="rounded-xl border border-zinc-200 bg-zinc-50 p-3 dark:border-white/[0.06] dark:bg-white/[0.03]">
                    <div className="flex items-center gap-2">
                      <AgentAvatar size={20} />
                      <span className="text-[12px] font-medium text-[#3F3F46] dark:text-white/85">
                        Aria is working
                      </span>
                      {step < 7 ? (
                        <span className="ml-auto flex items-center gap-1.5 text-[11px] text-[#71717A] dark:text-white/45">
                          <span
                            className="size-1.5 animate-pulse rounded-full"
                            style={{ backgroundColor: ACCENT }}
                          />
                          running
                        </span>
                      ) : (
                        <span className="ml-auto flex items-center gap-1 text-[11px] text-emerald-300">
                          <Check className="size-3" /> done
                        </span>
                      )}
                    </div>
                    <div className="mt-2.5 space-y-1.5 font-mono text-[12px]">
                      {TOOLS.map((row, i) => (
                        <div
                          key={row.tool}
                          className={cn(
                            "flex items-center gap-2 transition-all duration-300",
                            step >= 4 + i
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
                          step >= 7
                            ? "translate-y-0 text-emerald-600 opacity-100 dark:text-emerald-300/80"
                            : "translate-y-1 opacity-0",
                        )}
                      >
                        <span className="shrink-0 text-[#A1A1AA] dark:text-white/35">›</span>
                        <span className="truncate">
                          12 tests passed · 0.41s
                        </span>
                      </div>
                    </div>
                  </div>
                ) : null}

                {/* PR shipped */}
                {step >= 7 ? (
                  <div
                    className="flex items-center gap-2 rounded-xl border px-3 py-2.5 text-[13px]"
                    style={{
                      borderColor: `${ACCENT}55`,
                      backgroundColor: `${ACCENT}14`,
                    }}
                  >
                    <GitPullRequest
                      className="size-4"
                      style={{ color: ACCENT }}
                    />
                    <span className="text-[#3F3F46] dark:text-white/85">PR #214 — RTL polish</span>
                    <span className="ml-auto text-[11px] text-emerald-600 dark:text-emerald-300/80">
                      ready to merge
                    </span>
                  </div>
                ) : null}

                {/* Composer */}
                <div className="!mt-4 flex items-center gap-2 rounded-xl border border-zinc-200 bg-zinc-50 px-3 py-2 dark:border-white/10 dark:bg-white/[0.03]">
                  <input
                    readOnly
                    value=""
                    placeholder="Reply to Aria…"
                    className="flex-1 bg-transparent text-[13px] text-[#3F3F46] placeholder:text-[#A1A1AA] focus:outline-none dark:text-white/70 dark:placeholder:text-white/30"
                  />
                  <span
                    className="grid size-6 place-items-center rounded-md text-white"
                    style={{ backgroundColor: ACCENT }}
                  >
                    <ArrowUp className="size-3.5" />
                  </span>
                </div>
              </div>

              {/* Properties sidebar */}
              <div className="space-y-4 p-5 text-[12px]">
                <PropRow label="Status">
                  <span className={cn("rounded px-1.5 py-0.5", statusTone)}>
                    {status}
                  </span>
                </PropRow>
                <PropRow label="Assignee">
                  <span className="flex items-center gap-1.5 text-[#3F3F46] dark:text-white/80">
                    <AgentAvatar size={18} /> Aria
                  </span>
                </PropRow>
                <PropRow label="Priority">
                  <span className="text-[#52525B] dark:text-white/70">High</span>
                </PropRow>
                <PropRow label="Project">
                  <span className="text-[#52525B] dark:text-white/70">i18n</span>
                </PropRow>
              </div>
            </div>
          </div>
        </Reveal>
      </div>
    </section>
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
          "max-w-[78%] rounded-2xl px-3 py-2 text-[13px] leading-snug",
          side === "right"
            ? "rounded-br-sm bg-zinc-100 dark:bg-white/10"
            : "rounded-bl-sm border border-zinc-200 bg-zinc-50 dark:border-white/[0.06] dark:bg-white/[0.03]",
        )}
      >
        {children}
      </div>
    </div>
  );
}

function Typing() {
  return (
    <span className="flex items-center gap-1 py-1">
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className="size-1.5 animate-bounce rounded-full bg-[#A1A1AA] dark:bg-white/50"
          style={{ animationDelay: `${i * 0.15}s` }}
        />
      ))}
    </span>
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

function PropRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="w-16 shrink-0 text-[#71717A] dark:text-white/40">{label}</span>
      {children}
    </div>
  );
}
