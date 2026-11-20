"use client";

import Link from "next/link";
import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import { Reveal } from "./reveal";
import {
  containerClassName,
  sectionClassName,
  sectionTitleClassName,
  serifClassName,
  ui,
  useStartHref,
} from "./shared";

export function FAQSection() {
  const { t } = useLocale();

  return (
    <section id="faq" className={sectionClassName}>
      <div className={cn(containerClassName, "flex max-w-[800px] flex-col gap-8")}>
        <Reveal>
          <h2 className={sectionTitleClassName}>{t.faq.headline}</h2>
        </Reveal>
        <div className="flex flex-col gap-3">
          {t.faq.items.map((item, i) => (
            <Reveal key={item.question} delay={i * 50}>
              <details
                open={i === 0}
                className={cn("group rounded-[14px] border px-5 py-1.5 sm:px-[22px]", ui.surface, ui.line)}
              >
                <summary className="flex cursor-pointer list-none items-center justify-between gap-4 py-3.5 text-[18px] font-semibold [&::-webkit-details-marker]:hidden">
                  {item.question}
                  <span
                    aria-hidden
                    className={cn(
                      "flex size-6 flex-none items-center justify-center rounded-full border transition-transform group-open:rotate-45",
                      ui.lineStrong,
                      ui.muted,
                    )}
                  >
                    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
                      <path d="M6 1v10M1 6h10" />
                    </svg>
                  </span>
                </summary>
                <p className={cn("m-0 mb-[18px]", ui.muted)}>{item.answer}</p>
              </details>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  );
}

export function FinalCtaSection() {
  const { t } = useLocale();
  const startHref = useStartHref();
  const c = t.finalCta;

  return (
    <section id="start" className="px-4 pb-20 sm:px-6 sm:pb-[104px]">
      <Reveal
        className={cn(
          containerClassName,
          "flex flex-wrap items-center gap-x-16 gap-y-8 rounded-[24px] bg-[#2563EB] p-[clamp(2rem,6vw,5rem)] text-white",
        )}
      >
        <div className="flex min-w-0 flex-[1.4_1_380px] flex-col gap-3.5">
          <h2
            className={cn(
              serifClassName,
              "m-0 text-[clamp(1.875rem,3.6vw,3rem)] leading-[1.08] tracking-[-0.01em]",
            )}
          >
            {c.title}
          </h2>
          <p className="m-0 text-[18px] text-white">{c.body}</p>
        </div>
        <div className="flex flex-[0_1_auto] flex-wrap gap-3">
          <Link
            href={startHref}
            className="inline-flex min-h-11 items-center justify-center rounded-[12px] bg-white px-6 py-3 text-[15px] font-semibold text-[#05060B] transition-colors hover:bg-white/90"
          >
            {c.cta}
          </Link>
          <Link
            href="/contact-sales"
            className="inline-flex min-h-11 items-center justify-center rounded-[12px] border border-white px-6 py-3 text-[15px] font-semibold text-white transition-colors hover:bg-white/10"
          >
            {c.talkToSales}
          </Link>
        </div>
      </Reveal>
    </section>
  );
}
