"use client";

import Link from "next/link";
import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import { Reveal } from "./reveal";
import {
  CheckGlyph,
  SectionIntro,
  containerClassName,
  primaryButtonClassName,
  sectionClassName,
  secondaryButtonClassName,
  ui,
  useStartHref,
} from "./shared";

export function PricingSection() {
  const { t } = useLocale();
  const p = t.pricing;
  const startHref = useStartHref();

  return (
    <section id="pricing" className={cn(sectionClassName, "scroll-mt-4 border-b", ui.line)}>
      <div className={cn(containerClassName, "flex max-w-[960px] flex-col gap-12")}>
        <Reveal>
          <SectionIntro label={p.label} title={p.title} body={p.subtitle} center />
        </Reveal>

        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,300px),1fr))] gap-5">
          <Reveal
            className={cn("flex flex-col gap-[18px] rounded-[18px] border p-6 sm:p-[30px]", ui.surface, ui.line)}
          >
            <PlanHead name={p.free.name} description={p.free.description} />
            <p className="m-0 flex items-baseline gap-2">
              <span className="text-[44px] font-semibold leading-none">{p.free.price}</span>
              <span className={cn("text-[15px]", ui.muted)}>{p.free.period}</span>
            </p>
            <Features items={p.free.features} />
            <Link href={startHref} className={cn(secondaryButtonClassName(), "mt-auto")}>
              {p.free.cta}
            </Link>
          </Reveal>

          <Reveal
            delay={100}
            className={cn(
              "flex flex-col gap-[18px] rounded-[18px] border border-[#2563EB] p-6 sm:p-[30px]",
              ui.surface,
            )}
          >
            <PlanHead name={p.team.name} description={p.team.description} badge={p.team.badge} />
            <div className="flex flex-col gap-1">
              <p className="m-0 flex flex-wrap items-baseline gap-2">
                <span className="text-[44px] font-semibold leading-none">{p.team.price}</span>
                <span className={cn("text-[15px]", ui.muted)}>{p.team.period}</span>
              </p>
              <p className={cn("m-0 text-[14px]", ui.muted)}>{p.team.monthly}</p>
            </div>
            <Features items={p.team.features} />
            <Link href={startHref} className={cn(primaryButtonClassName(), "mt-auto")}>
              {p.team.cta}
            </Link>
          </Reveal>
        </div>

        <p className={cn("m-0 text-center text-[15px]", ui.muted)}>
          {p.trialNote} {p.needMore}{" "}
          <Link href="/contact-sales" className={cn("font-semibold", ui.eyebrow)}>
            {p.contactSales}
          </Link>
        </p>
      </div>
    </section>
  );
}

function PlanHead({
  name,
  description,
  badge,
}: {
  name: string;
  description: string;
  badge?: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-2.5">
        <h3 className="m-0 text-[20px] font-semibold">{name}</h3>
        {badge ? (
          <span
            className={cn(
              "rounded-full px-2.5 py-1 text-[12px] font-semibold",
              ui.accentSoft,
              ui.eyebrow,
            )}
          >
            {badge}
          </span>
        ) : null}
      </div>
      <p className={cn("m-0 text-[15px]", ui.muted)}>{description}</p>
    </div>
  );
}

function Features({ items }: { items: string[] }) {
  return (
    <ul className={cn("m-0 flex list-none flex-col gap-2 p-0", ui.soft)}>
      {items.map((item) => (
        <li key={item} className="flex items-start gap-2.5">
          <CheckGlyph className={cn("mt-1 size-4 flex-none", ui.eyebrow)} />
          <span>{item}</span>
        </li>
      ))}
    </ul>
  );
}
