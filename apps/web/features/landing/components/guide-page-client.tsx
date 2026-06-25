"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import {
  ArrowRight,
  Bot,
  Cloud,
  GitPullRequest,
  ListChecks,
  Monitor,
  Plus,
  Sparkles,
  UserPlus,
} from "lucide-react";
import { useAuthStore } from "@agora/core/auth";
import { cn } from "@agora/ui/lib/utils";
import { LandingHeader } from "./landing-header";
import { LandingFooter } from "./landing-footer";
import { Reveal } from "./reveal";

const ACCENT = "#2563EB";

type Step = {
  label: string;
  title: string;
  description: string;
  points: string[];
  icon: React.ComponentType<{ className?: string }>;
  visual: React.ComponentType;
};

const STEPS: Step[] = [
  {
    label: "Workspace",
    title: "Create your workspace",
    description:
      "A workspace is your team's home — issues, projects, members, and agents all live inside it. Pick a name and an issue prefix; everything else is ready out of the box.",
    points: [
      "Invite teammates by email or link.",
      "Set a short issue prefix (e.g. AGORA-) for ticket IDs.",
      "Switch between workspaces anytime from the top-left.",
    ],
    icon: Plus,
    visual: WorkspaceVisual,
  },
  {
    label: "Runtime",
    title: "Connect a runtime",
    description:
      "Agents need somewhere to run. Connect a local daemon on your own machine for full control, or a cloud runtime for zero-setup execution. Both show up side by side.",
    points: [
      "Local: run `agora daemon` — your code never leaves your machine.",
      "Cloud: managed execution, nothing to install.",
      "Mix and match — route each task to the right runtime.",
    ],
    icon: Monitor,
    visual: RuntimeVisual,
  },
  {
    label: "Create issue",
    title: "Create your first issue",
    description:
      "Issues are the unit of work. Give it a title, a description, a priority — the same shape whether a human or an agent will pick it up.",
    points: [
      "Press C anywhere to open the composer.",
      "Add context, links, and acceptance criteria in the description.",
      "Group issues into projects and sprints.",
    ],
    icon: ListChecks,
    visual: IssueVisual,
  },
  {
    label: "Assign",
    title: "Assign it to a person — or an agent",
    description:
      "The assignee field is polymorphic. Drop in a teammate or a coding agent through the exact same picker. The agent starts the moment it's assigned.",
    points: [
      "Agents render with a distinct emerald avatar.",
      "Reassign between human and agent at any point.",
      "Status moves to In Progress automatically when an agent picks up.",
    ],
    icon: UserPlus,
    visual: AssignVisual,
  },
  {
    label: "Watch",
    title: "Watch the agent work",
    description:
      "Every tool call, file read, and edit streams live onto the issue. No black box — you see exactly what the agent is doing as it happens.",
    points: [
      "Live activity feed with thinking, tool calls, and results.",
      "Token usage and run history per agent.",
      "Stop or redirect a run whenever you need to.",
    ],
    icon: Bot,
    visual: WatchVisual,
  },
  {
    label: "Review",
    title: "Review, comment & ship",
    description:
      "When the agent finishes, the work lands as a pull request. Comment with @mentions, request changes, and the agent iterates — right on the issue.",
    points: [
      "@mention an agent to send it back to work.",
      "Threaded comments keep humans and agents in one place.",
      "Merge the PR and the issue closes itself.",
    ],
    icon: GitPullRequest,
    visual: ReviewVisual,
  },
  {
    label: "Skills",
    title: "Compound your team's skills",
    description:
      "Capture a repeatable workflow once as a Skill — deploy steps, migration recipes, review checklists — and every agent on the team can run it. Your team gets sharper over time.",
    points: [
      "Skills are versioned files agents can read and run.",
      "Share across the workspace, no copy-paste.",
      "Improve a skill once; every agent benefits.",
    ],
    icon: Sparkles,
    visual: SkillsVisual,
  },
];

export function GuidePageClient() {
  const user = useAuthStore((s) => s.user);
  const [activeIndex, setActiveIndex] = useState(0);
  const panelRefs = useRef<(HTMLDivElement | null)[]>([]);

  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            const idx = Number(entry.target.getAttribute("data-index"));
            if (!Number.isNaN(idx)) setActiveIndex(idx);
          }
        }
      },
      { rootMargin: "-25% 0px -60% 0px", threshold: 0 },
    );
    panelRefs.current.forEach((el) => el && observer.observe(el));
    return () => observer.disconnect();
  }, []);

  const scrollToPanel = (index: number) => {
    panelRefs.current[index]?.scrollIntoView({
      behavior: "smooth",
      block: "start",
    });
  };

  return (
    <div className="bg-[#05060b] text-white">
      <div className="relative">
        <LandingHeader variant="dark" />

        {/* Hero */}
        <section className="relative overflow-hidden">
          <GuideBackdrop />
          <div className="relative z-10 mx-auto max-w-[1120px] px-4 pb-16 pt-32 text-center sm:px-6 sm:pt-40 lg:px-8 lg:pb-24">
            <Reveal from="none">
              <p className="text-[12px] font-medium uppercase tracking-[0.34em] text-white/40">
                Getting started <span className="text-white/25">·</span>{" "}
                <span style={{ color: ACCENT }}>7 steps</span>
              </p>
            </Reveal>
            <Reveal delay={80}>
              <h1 className="mx-auto mt-5 max-w-[900px] font-[family-name:var(--font-serif)] text-[3rem] leading-[1.0] tracking-[-0.035em] drop-shadow-[0_10px_34px_rgba(0,0,0,0.32)] sm:text-[4rem] lg:text-[5rem]">
                How Agora works
              </h1>
            </Reveal>
            <Reveal delay={160}>
              <p className="mx-auto mt-6 max-w-[640px] text-[15px] leading-7 text-white/80 sm:text-[17px]">
                From an empty workspace to a coding agent shipping a pull
                request — here&apos;s the whole loop, end to end.
              </p>
            </Reveal>
            <Reveal delay={240}>
              <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
                <Link
                  href={user ? "/" : "/login"}
                  className="group inline-flex items-center justify-center gap-2 rounded-[12px] px-5 py-3 text-[14px] font-semibold text-white shadow-[0_14px_40px_-12px_rgba(37,99,235,0.7)] transition-transform hover:-translate-y-0.5"
                  style={{ backgroundColor: ACCENT }}
                >
                  {user ? "Open dashboard" : "Start free"}
                  <ArrowRight
                    className="size-4 transition-transform group-hover:translate-x-0.5"
                    aria-hidden
                  />
                </Link>
                <Link
                  href="#step-0"
                  className="inline-flex items-center justify-center rounded-[12px] border border-white/14 bg-white/[0.04] px-5 py-3 text-[14px] font-semibold text-white/85 transition-colors hover:bg-white/[0.08] hover:text-white"
                >
                  Read the guide
                </Link>
              </div>
            </Reveal>
          </div>
        </section>
      </div>

      {/* Steps */}
      <section className="relative border-t border-white/8">
        <div className="mx-auto max-w-[1320px] px-4 sm:px-6 lg:px-8">
          <div className="relative lg:flex lg:gap-20">
            {/* Sticky step rail */}
            <nav className="hidden lg:block lg:w-[200px] lg:shrink-0">
              <div className="sticky top-28 flex flex-col gap-0 py-28">
                {STEPS.map((s, i) => (
                  <button
                    type="button"
                    key={s.label}
                    onClick={() => scrollToPanel(i)}
                    className={cn(
                      "group flex items-center gap-3 rounded-lg px-3 py-2.5 text-left text-[12px] font-medium transition-colors",
                      i === activeIndex
                        ? "text-white"
                        : "text-white/35 hover:text-white/65",
                    )}
                  >
                    <span
                      className={cn(
                        "grid size-6 shrink-0 place-items-center rounded-md text-[11px] font-semibold tabular-nums transition-colors",
                        i === activeIndex
                          ? "text-white"
                          : "bg-white/[0.06] text-white/45",
                      )}
                      style={
                        i === activeIndex
                          ? { backgroundColor: ACCENT }
                          : undefined
                      }
                    >
                      {i + 1}
                    </span>
                    {s.label}
                  </button>
                ))}
              </div>
            </nav>

            {/* Step panels */}
            <div className="flex-1">
              {STEPS.map((step, i) => {
                const Icon = step.icon;
                const Visual = step.visual;
                return (
                  <div
                    key={step.label}
                    id={`step-${i}`}
                    ref={(el) => {
                      panelRefs.current[i] = el;
                    }}
                    data-index={i}
                    className={cn(
                      "scroll-mt-24 py-20 lg:py-28",
                      i < STEPS.length - 1 && "border-b border-white/8",
                    )}
                  >
                    <Reveal>
                      <div className="flex items-center gap-3">
                        <span
                          className="grid size-9 place-items-center rounded-xl text-white"
                          style={{
                            backgroundColor: `${ACCENT}1f`,
                            color: ACCENT,
                          }}
                        >
                          <Icon className="size-4" />
                        </span>
                        <span className="text-[12px] font-semibold uppercase tracking-[0.22em] text-white/40">
                          Step {i + 1}
                        </span>
                      </div>
                    </Reveal>

                    <Reveal delay={60}>
                      <h2 className="mt-5 font-[family-name:var(--font-serif)] text-[2.3rem] leading-[1.05] tracking-[-0.03em] sm:text-[3rem] lg:text-[3.4rem]">
                        {step.title}
                      </h2>
                    </Reveal>
                    <Reveal delay={120}>
                      <p className="mt-5 max-w-[620px] text-[15px] leading-7 text-white/65 sm:text-[16px]">
                        {step.description}
                      </p>
                    </Reveal>

                    <Reveal delay={180}>
                      <ul className="mt-6 max-w-[620px] space-y-3">
                        {step.points.map((point) => (
                          <li
                            key={point}
                            className="flex items-start gap-3 text-[14px] leading-6 text-white/75"
                          >
                            <span
                              className="mt-2 size-1.5 shrink-0 rounded-full"
                              style={{ backgroundColor: ACCENT }}
                              aria-hidden
                            />
                            {point}
                          </li>
                        ))}
                      </ul>
                    </Reveal>

                    <Reveal className="mt-12">
                      <Visual />
                    </Reveal>
                  </div>
                );
              })}
            </div>
          </div>
        </div>

        {/* Closing CTA */}
        <div className="mx-auto max-w-[1120px] px-4 pb-28 pt-8 text-center sm:px-6 lg:px-8">
          <Reveal>
            <div className="overflow-hidden rounded-3xl border border-white/10 bg-white/[0.03] px-6 py-16">
              <h2 className="font-[family-name:var(--font-serif)] text-[2.2rem] leading-[1.05] tracking-[-0.03em] sm:text-[3rem]">
                Put an agent on your board
              </h2>
              <p className="mx-auto mt-4 max-w-[520px] text-[15px] leading-7 text-white/65">
                Spin up a workspace and assign your first issue to an agent in
                under five minutes.
              </p>
              <Link
                href={user ? "/" : "/login"}
                className="group mt-8 inline-flex items-center justify-center gap-2 rounded-[12px] px-6 py-3.5 text-[14px] font-semibold text-white shadow-[0_14px_40px_-12px_rgba(37,99,235,0.7)] transition-transform hover:-translate-y-0.5"
                style={{ backgroundColor: ACCENT }}
              >
                {user ? "Open dashboard" : "Start free"}
                <ArrowRight
                  className="size-4 transition-transform group-hover:translate-x-0.5"
                  aria-hidden
                />
              </Link>
            </div>
          </Reveal>
        </div>
      </section>

      <LandingFooter />
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Shared frame + step visuals */
/* ------------------------------------------------------------------ */

function GuideBackdrop() {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0">
      <div
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(120% 80% at 50% -10%, rgba(37,99,235,0.28), transparent 55%)",
        }}
      />
      <div
        className="absolute inset-0 opacity-50"
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
    </div>
  );
}

function Frame({
  children,
  title,
}: {
  children: React.ReactNode;
  title: string;
}) {
  return (
    <div className="max-w-[680px] overflow-hidden rounded-2xl border border-white/10 bg-[#0a0c16]/80 shadow-[0_40px_120px_-30px_rgba(37,99,235,0.4)]">
      <div className="flex items-center gap-2 border-b border-white/8 px-4 py-2.5">
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="ml-2 text-[12px] text-white/40">{title}</span>
      </div>
      <div className="p-4">{children}</div>
    </div>
  );
}

function AgentAvatar({ size = 20 }: { size?: number }) {
  return (
    <span
      className="inline-flex items-center justify-center rounded-full text-white"
      style={{ width: size, height: size, backgroundColor: ACCENT }}
    >
      <Sparkles style={{ width: size * 0.5, height: size * 0.5 }} />
    </span>
  );
}

function HumanAvatar({
  initials,
  size = 20,
}: {
  initials: string;
  size?: number;
}) {
  return (
    <span
      className="inline-flex items-center justify-center rounded-full bg-white/10 font-semibold text-white/80"
      style={{ width: size, height: size, fontSize: size * 0.4 }}
    >
      {initials}
    </span>
  );
}

function WorkspaceVisual() {
  return (
    <Frame title="agora.dev">
      <div className="flex items-center gap-3">
        <span
          className="grid size-10 place-items-center rounded-lg text-[15px] font-semibold text-white"
          style={{ backgroundColor: ACCENT }}
        >
          SD
        </span>
        <div>
          <div className="text-[14px] font-medium text-white/90">
            Sales Doctor
          </div>
          <div className="text-[12px] text-white/45">
            agora.dev/salesdoctor · prefix AGORA-
          </div>
        </div>
      </div>
      <div className="mt-4 flex -space-x-2">
        {["JT", "DV", "AK"].map((m) => (
          <HumanAvatar key={m} initials={m} size={26} />
        ))}
        <AgentAvatar size={26} />
        <span className="ml-3 self-center text-[12px] text-white/45">
          3 members · 1 agent
        </span>
      </div>
    </Frame>
  );
}

function RuntimeVisual() {
  const runtimes = [
    {
      name: "MacBook Pro",
      sub: "local · arm64 / macOS",
      icon: Monitor,
      online: true,
    },
    {
      name: "Cloud (Anthropic)",
      sub: "managed · zero setup",
      icon: Cloud,
      online: true,
    },
  ];
  return (
    <Frame title="Runtimes">
      <div className="space-y-2">
        {runtimes.map((rt) => {
          const Icon = rt.icon;
          return (
            <div
              key={rt.name}
              className="flex items-center gap-3 rounded-lg border border-white/[0.06] bg-white/[0.03] px-3 py-2.5"
            >
              <span className="grid size-8 place-items-center rounded-md bg-white/[0.06]">
                <Icon className="size-4 text-white/70" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-medium text-white/90">
                  {rt.name}
                </div>
                <div className="text-[11px] text-white/45">{rt.sub}</div>
              </div>
              <span className="flex items-center gap-1.5 text-[11px] text-emerald-300/80">
                <span className="size-1.5 rounded-full bg-emerald-400" /> online
              </span>
            </div>
          );
        })}
      </div>
    </Frame>
  );
}

function IssueVisual() {
  return (
    <Frame title="New issue">
      <div className="text-[11px] font-medium tracking-wide text-white/35">
        AGORA-128
      </div>
      <div className="mt-1 text-[16px] font-semibold text-white/95">
        Persian localization — RTL polish
      </div>
      <p className="mt-2 text-[13px] leading-6 text-white/55">
        Audit every screen for right-to-left layout bugs. Mirror icons, fix
        number alignment, verify the issue board flips correctly.
      </p>
      <div className="mt-4 flex flex-wrap gap-2 text-[11px]">
        {["Priority: High", "Project: i18n", "Sprint: 14"].map((tag) => (
          <span
            key={tag}
            className="rounded-full border border-white/10 bg-white/[0.04] px-2.5 py-1 text-white/60"
          >
            {tag}
          </span>
        ))}
      </div>
    </Frame>
  );
}

function AssignVisual() {
  return (
    <Frame title="Assign to…">
      <div className="space-y-1">
        <div className="px-2 py-1 text-[10px] font-medium uppercase tracking-wider text-white/35">
          Members
        </div>
        {[
          { name: "JT · you", initials: "JT" },
          { name: "Dilnoza", initials: "DV" },
        ].map((m) => (
          <div
            key={m.name}
            className="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-[13px] text-white/80"
          >
            <HumanAvatar initials={m.initials} size={20} />
            {m.name}
          </div>
        ))}
        <div className="px-2 pt-2 text-[10px] font-medium uppercase tracking-wider text-white/35">
          Agents
        </div>
        <div
          className="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-[13px] text-white/90"
          style={{ backgroundColor: `${ACCENT}1a` }}
        >
          <AgentAvatar size={20} />
          Aria
          <span className="ml-auto text-[11px]" style={{ color: ACCENT }}>
            assigned ✓
          </span>
        </div>
      </div>
    </Frame>
  );
}

function WatchVisual() {
  const rows = [
    { tool: "Read", text: "server/internal/i18n/rtl.go" },
    { tool: "Edit", text: "mirror chevron + flip board columns" },
    { tool: "Bash", text: "go test ./internal/i18n/ -run TestRTL" },
    { tool: "result", text: "ok github.com/agora/server/internal/i18n 0.41s" },
  ];
  return (
    <Frame title="AGORA-128 · activity">
      <div className="flex items-center gap-2">
        <AgentAvatar size={22} />
        <span className="text-[13px] font-medium text-white/90">
          Aria is working
        </span>
        <span className="ml-auto text-[11px] text-white/45">
          4m 02s · 12 tool calls
        </span>
      </div>
      <div className="mt-3 space-y-1.5 font-mono text-[12px]">
        {rows.map((r) => (
          <div key={r.text} className="flex items-center gap-2 text-white/55">
            <span
              className={cn(
                "shrink-0 font-semibold",
                r.tool === "result" ? "text-white/35" : "text-white/80",
              )}
            >
              {r.tool === "result" ? "›" : r.tool}
            </span>
            <span className="truncate">{r.text}</span>
          </div>
        ))}
      </div>
    </Frame>
  );
}

function ReviewVisual() {
  return (
    <Frame title="AGORA-128 · review">
      <div className="rounded-lg border border-white/[0.06] bg-white/[0.03] p-3">
        <div className="flex items-center gap-2 text-[13px]">
          <HumanAvatar initials="JT" size={22} />
          <span className="font-medium text-white/90">JT</span>
          <span className="text-[11px] text-white/40">just now</span>
        </div>
        <p className="mt-1.5 pl-8 text-[13px] leading-6 text-white/65">
          Looks great. <span style={{ color: ACCENT }}>@Aria</span> can you also
          flip the date picker?
        </p>
      </div>
      <div
        className="mt-2 flex items-center gap-2 rounded-lg border px-3 py-2.5 text-[13px]"
        style={{ borderColor: `${ACCENT}55`, backgroundColor: `${ACCENT}12` }}
      >
        <GitPullRequest className="size-4" style={{ color: ACCENT }} />
        <span className="text-white/85">PR #214 — RTL polish</span>
        <span className="ml-auto text-[11px] text-emerald-300/80">
          ready to merge
        </span>
      </div>
    </Frame>
  );
}

function SkillsVisual() {
  const skills = [
    { name: "Write migration", desc: "Generate + validate SQL", on: true },
    { name: "Deploy to staging", desc: "Run staging pipeline", on: false },
    { name: "Review PR", desc: "Style-guide checks", on: false },
  ];
  return (
    <Frame title="Skills">
      <div className="space-y-2">
        {skills.map((s) => (
          <div
            key={s.name}
            className={cn(
              "flex items-center gap-3 rounded-lg border px-3 py-2.5",
              s.on ? "border-white/10" : "border-white/[0.06] bg-white/[0.02]",
            )}
            style={s.on ? { backgroundColor: `${ACCENT}12` } : undefined}
          >
            <span className="grid size-8 place-items-center rounded-md bg-white/[0.06]">
              <Sparkles
                className="size-4"
                style={{ color: s.on ? ACCENT : "rgba(255,255,255,0.5)" }}
              />
            </span>
            <div className="min-w-0 flex-1">
              <div className="text-[13px] font-medium text-white/90">
                {s.name}
              </div>
              <div className="text-[11px] text-white/45">{s.desc}</div>
            </div>
            <span className="text-[11px] text-white/40">v1.2</span>
          </div>
        ))}
      </div>
    </Frame>
  );
}
