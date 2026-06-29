import { Plug, GitPullRequest, RefreshCw, Cloud } from "lucide-react";
import { Reveal } from "./reveal";

// Royal-blue brand accent, hardcoded like the rest of the landing (renders in the
// landing-light scope, outside the token system — see CLAUDE.md Brand color).
const ACCENT = "#2563EB";

// Secondary capability grid: surfaces the newer platform features the four deep
// feature blocks above don't cover. English-only for now (the landing falls back
// to English for non-en locales); add i18n keys when these stabilize.
const capabilities = [
  {
    icon: Plug,
    title: "Connect your tools",
    body: "Import issues from Bitrix, link Lark / Feishu chat, and connect your repositories. Agents pick up work where your team already is — no migration.",
  },
  {
    icon: GitPullRequest,
    title: "Review and ship in-app",
    body: "Watch agents code live in an embedded editor, read the diff, run QA gates, and merge as a pull request — without leaving the board.",
  },
  {
    icon: RefreshCw,
    title: "Autopilots & self-updating docs",
    body: "Schedule or trigger automations — QA on every deploy, a weekly docs sweep. Every change documents itself back into your knowledge base.",
  },
  {
    icon: Cloud,
    title: "Always-on cloud agents",
    body: "Agents run around the clock on cloud runtimes — not tied to your laptop — across all your repositories and accounts.",
  },
];

export function CapabilitiesSection() {
  return (
    <section className="bg-white text-[#18181B] dark:bg-[#05070b] dark:text-white">
      <div className="mx-auto max-w-[1320px] px-4 py-24 sm:px-6 sm:py-32 lg:px-8 lg:py-40">
        <Reveal>
          <h2 className="max-w-2xl text-3xl font-semibold tracking-tight sm:text-4xl">
            More than a board — a place your whole workflow runs
          </h2>
        </Reveal>
        <Reveal delay={80}>
          <p className="mt-4 max-w-2xl text-base text-zinc-600 dark:text-zinc-400">
            Bring your existing tools, review and merge agent work in one place, and
            let automations keep everything moving.
          </p>
        </Reveal>
        <div className="mt-16 grid gap-px overflow-hidden rounded-2xl border border-zinc-200 bg-zinc-200 dark:border-white/10 dark:bg-white/10 sm:grid-cols-2 lg:grid-cols-4">
          {capabilities.map((c) => (
            <div
              key={c.title}
              className="flex flex-col gap-4 bg-white p-7 dark:bg-[#05070b]"
            >
              <div
                className="grid size-11 place-items-center rounded-xl"
                style={{ backgroundColor: `${ACCENT}14`, color: ACCENT }}
              >
                <c.icon className="size-5" strokeWidth={1.75} />
              </div>
              <h3 className="text-lg font-semibold tracking-tight">{c.title}</h3>
              <p className="text-sm leading-relaxed text-zinc-600 dark:text-zinc-400">
                {c.body}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
