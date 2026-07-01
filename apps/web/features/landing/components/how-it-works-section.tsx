"use client";

import Link from "next/link";
import { useAuthStore } from "@agora/core/auth";
import { docsHrefForLocale, useLocale } from "../i18n";
import { heroButtonClassName } from "./shared";
import { Reveal } from "./reveal";

export function HowItWorksSection() {
  const { t, locale } = useLocale();
  const user = useAuthStore((s) => s.user);

  return (
    <section id="how-it-works" className="bg-white text-[#18181B] dark:bg-[#05070b] dark:text-white">
      <div className="mx-auto max-w-[1320px] px-4 py-24 sm:px-6 sm:py-32 lg:px-8 lg:py-40">
        <Reveal>
          <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-[#71717A] dark:text-white/40">
            {t.howItWorks.label}
          </p>
        </Reveal>
        <Reveal delay={80}>
          <h2 className="mt-4 font-[family-name:var(--font-serif)] text-[2.6rem] leading-[1.05] tracking-[-0.03em] sm:text-[3.4rem] lg:text-[4.2rem]">
            {t.howItWorks.headlineMain}
          </h2>
        </Reveal>

        <div className="mt-20 grid gap-px overflow-hidden rounded-2xl border border-zinc-200 bg-zinc-200 dark:border-white/10 dark:bg-white/10 sm:grid-cols-2 lg:grid-cols-4">
          {t.howItWorks.steps.map((step, i) => (
            <Reveal key={i} delay={i * 90} className="flex flex-col bg-white p-8 dark:bg-[#05070b] lg:p-10">
              <span
                className="text-[13px] font-semibold tabular-nums"
                style={{ color: "#2563EB", opacity: 0.55 }}
              >
                {String(i + 1).padStart(2, "0")}
              </span>
              <h3 className="mt-4 text-[17px] font-semibold leading-snug text-[#18181B] dark:text-white sm:text-[18px]">
                {step.title}
              </h3>
              <p className="mt-3 text-[14px] leading-[1.7] text-[#71717A] dark:text-white/50 sm:text-[15px]">
                {step.description}
              </p>
            </Reveal>
          ))}
        </div>

        <div className="mt-14 flex flex-wrap items-center gap-4">
          <Link
            href={user ? "/" : "/login"}
            className={heroButtonClassName("solid")}
          >
            {user ? t.header.dashboard : t.howItWorks.cta}
          </Link>
          <Link
            href={docsHrefForLocale(locale)}
            className={heroButtonClassName("ghost")}
          >
            {t.howItWorks.ctaDocs}
          </Link>
        </div>
      </div>
    </section>
  );
}
