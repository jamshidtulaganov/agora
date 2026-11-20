"use client";

import { cn } from "@agora/ui/lib/utils";
import { useLocale } from "../i18n";
import { Reveal } from "./reveal";
import {
  SectionIntro,
  containerClassName,
  sectionClassName,
  serifClassName,
  ui,
} from "./shared";

// Tool names are brands: never translated.
const TOOLS = [
  "Zoho CRM",
  "Zoho Desk",
  "Zoho Projects",
  "Bitrix24",
  "Lark / Feishu",
  "Telegram",
  "Figma",
];

/** "What is inside": knowledge, assistant, automations. */
export function InsideSection() {
  const { t } = useLocale();
  const s = t.inside;
  const a = t.automation;
  const bubble = cn("m-0 rounded-[10px] border px-3 py-2.5 text-[14px]", ui.card, ui.lineStrong);

  return (
    <section id="product" className={sectionClassName}>
      <div className={cn(containerClassName, "flex flex-col gap-12")}>
        <Reveal>
          <SectionIntro label={s.label} title={s.title} />
        </Reveal>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,300px),1fr))] gap-6">
          <FeatureCard title={s.knowledge.title} body={s.knowledge.body} delay={0}>
            <div className="flex flex-wrap gap-2">
              {s.knowledge.files.map((f) => (
                <span
                  key={f}
                  className={cn("rounded-[8px] border px-2.5 py-1.5 text-[13px] font-medium", ui.card, ui.lineStrong)}
                >
                  {f}
                </span>
              ))}
            </div>
            <p className={bubble}>
              {s.knowledge.quote}{" "}
              <span className={cn("font-semibold", ui.eyebrow)}>{s.knowledge.quoteSource}</span>
            </p>
          </FeatureCard>

          <FeatureCard title={s.assistant.title} body={s.assistant.body} delay={80}>
            <p className="m-0 max-w-[85%] self-end rounded-[12px_12px_2px_12px] bg-[#2563EB] px-3 py-2.5 text-[14px] text-white">
              {s.assistant.userMsg}
            </p>
            <p className={cn(bubble, "max-w-[85%] self-start rounded-[12px_12px_12px_2px]")}>
              {s.assistant.reply}
            </p>
          </FeatureCard>

          <FeatureCard title={s.automations.title} body={s.automations.body} delay={160}>
            {[
              [a.when, a.whenValue],
              [a.if, a.ifValue],
              [a.then, a.thenValue],
            ].map(([k, v]) => (
              <p key={k} className={cn(bubble, "py-2")}>
                <span className={cn("font-bold", ui.eyebrow)}>{k}</span> {v}
              </p>
            ))}
          </FeatureCard>
        </div>
      </div>
    </section>
  );
}

function FeatureCard({
  title,
  body,
  delay,
  children,
}: {
  title: string;
  body: string;
  delay: number;
  children: React.ReactNode;
}) {
  return (
    <Reveal
      delay={delay}
      className={cn("flex flex-col overflow-hidden rounded-[16px] border", ui.line)}
    >
      <div className={cn("flex min-h-[170px] flex-col justify-center gap-2.5 p-6", ui.surface)}>
        {children}
      </div>
      <div className="flex flex-col gap-2.5 p-6">
        <h3 className="m-0 text-[20px] font-semibold">{title}</h3>
        <p className={cn("m-0", ui.muted)}>{body}</p>
      </div>
    </Reveal>
  );
}

export function ControlSection() {
  const { t } = useLocale();
  const c = t.control;
  return (
    <section id="control" className={cn(sectionClassName, "border-y", ui.surface, ui.line)}>
      <div className={cn(containerClassName, "flex flex-wrap gap-x-16 gap-y-12")}>
        <Reveal className="min-w-0 flex-[1_1_340px]">
          <SectionIntro label={c.label} title={c.title} body={c.body} />
        </Reveal>
        <div className="grid min-w-0 flex-[1.4_1_480px] grid-cols-[repeat(auto-fit,minmax(min(100%,240px),1fr))] gap-4">
          {c.items.map((item, i) => (
            <Reveal
              key={item.title}
              delay={i * 60}
              className={cn("flex flex-col gap-2 rounded-[14px] border p-[22px]", ui.card, ui.line)}
            >
              <h3 className="m-0 text-[18px] font-semibold">{item.title}</h3>
              <p className={cn("m-0 text-[15px]", ui.muted)}>{item.body}</p>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  );
}

export function ToolsSection() {
  const { t } = useLocale();
  return (
    <section id="tools" className={cn("border-b px-4 py-[72px] sm:px-6 sm:py-[88px]", ui.line)}>
      <div className={cn(containerClassName, "flex flex-wrap items-center gap-x-16 gap-y-7")}>
        <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-2.5">
          <h2
            className={cn(
              serifClassName,
              "m-0 text-[clamp(1.625rem,2.6vw,2.125rem)] leading-[1.15]",
            )}
          >
            {t.tools.title}
          </h2>
          <p className={cn("m-0", ui.muted)}>{t.tools.body}</p>
        </div>
        <div className="flex min-w-0 flex-[1.4_1_420px] flex-wrap gap-2.5">
          {TOOLS.map((tool) => (
            <span
              key={tool}
              className={cn("rounded-full border px-4 py-2 text-[15px] font-medium", ui.lineStrong)}
            >
              {tool}
            </span>
          ))}
          <span
            className={cn(
              "rounded-full border border-dashed border-[#7C879B] px-4 py-2 text-[15px] font-medium",
              ui.muted,
            )}
          >
            {t.tools.anyMcp}
          </span>
        </div>
      </div>
    </section>
  );
}
