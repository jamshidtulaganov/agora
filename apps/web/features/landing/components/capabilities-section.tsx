"use client";

import { Plug, GitPullRequest, RefreshCw, Cloud } from "lucide-react";
import { Reveal } from "./reveal";
import { useLocale } from "../i18n";

// Royal-blue brand accent, hardcoded like the rest of the landing (renders in the
// landing-light scope, outside the token system — see CLAUDE.md Brand color).
const ACCENT = "#2563EB";

const ICONS = [Plug, GitPullRequest, RefreshCw, Cloud];

// Secondary capability grid: surfaces the newer platform features the deep
// feature blocks above don't cover.
export function CapabilitiesSection() {
  const { t } = useLocale();
  return (
    <section className="bg-white text-[#18181B] dark:bg-[#05070b] dark:text-white">
      <div className="mx-auto max-w-[1320px] px-4 py-24 sm:px-6 sm:py-32 lg:px-8 lg:py-40">
        <Reveal>
          <h2 className="max-w-2xl text-3xl font-semibold tracking-tight sm:text-4xl">
            {t.capabilities.title}
          </h2>
        </Reveal>
        <Reveal delay={80}>
          <p className="mt-4 max-w-2xl text-base text-zinc-600 dark:text-zinc-400">
            {t.capabilities.subtitle}
          </p>
        </Reveal>
        <div className="mt-16 grid gap-px overflow-hidden rounded-2xl border border-zinc-200 bg-zinc-200 dark:border-white/10 dark:bg-white/10 sm:grid-cols-2 lg:grid-cols-4">
          {t.capabilities.items.map((c, i) => {
            const Icon = ICONS[i] ?? Plug;
            return (
              <Reveal
                key={c.title}
                delay={i * 80}
                className="group flex flex-col gap-4 bg-white p-7 transition-shadow duration-300 hover:shadow-[0_8px_32px_-8px_rgba(37,99,235,0.15)] dark:bg-[#05070b]"
              >
                <div
                  className="grid size-11 place-items-center rounded-xl transition-transform duration-300 group-hover:scale-110"
                  style={{ backgroundColor: `${ACCENT}14`, color: ACCENT }}
                >
                  <Icon className="size-5" strokeWidth={1.75} />
                </div>
                <h3 className="text-lg font-semibold tracking-tight">
                  {c.title}
                </h3>
                <p className="text-sm leading-relaxed text-zinc-600 dark:text-zinc-400">
                  {c.body}
                </p>
              </Reveal>
            );
          })}
        </div>
      </div>
    </section>
  );
}
