"use client";

import { useEffect, useRef, useState } from "react";

const ACCENT = "#2563EB";

const STATS = [
  { raw: 5, suffix: "", label: "AI runtimes" },
  { raw: 100, suffix: "%", label: "Open source" },
  { raw: 40, suffix: "+", label: "MCP tools" },
  { raw: 24, suffix: "/7", label: "Cloud agents" },
];

function useCountUp(target: number, active: boolean, duration = 1100) {
  const [value, setValue] = useState(0);
  useEffect(() => {
    if (!active) return;
    let startTs: number | null = null;
    const step = (ts: number) => {
      if (startTs === null) startTs = ts;
      const p = Math.min((ts - startTs) / duration, 1);
      setValue(Math.round(target * (1 - Math.pow(1 - p, 3))));
      if (p < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }, [active, target, duration]);
  return value;
}

function StatItem({
  raw,
  suffix,
  label,
  delay,
}: (typeof STATS)[0] & { delay: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(false);
  const count = useCountUp(raw, active);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const prefersReduced = window.matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    if (prefersReduced) {
      setActive(true);
      return;
    }
    const io = new IntersectionObserver(
      ([e]) => {
        if (e?.isIntersecting) {
          setTimeout(() => setActive(true), delay);
          io.disconnect();
        }
      },
      { threshold: 0.5 },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [delay]);

  return (
    <div ref={ref} className="flex flex-col items-center gap-2 text-center">
      <span
        className="text-[2.6rem] font-semibold leading-none tracking-tight text-[#18181B] dark:text-white sm:text-[3.2rem]"
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {count}
        <span style={{ color: ACCENT }}>{suffix}</span>
      </span>
      <span className="text-[13px] font-medium text-[#71717A] dark:text-white/45">
        {label}
      </span>
    </div>
  );
}

export function StatsStrip() {
  return (
    <section className="border-y border-zinc-100 bg-white dark:border-white/[0.06] dark:bg-[#05060b]">
      <div className="mx-auto max-w-[1320px] px-4 py-16 sm:px-6 lg:px-8">
        <div className="grid grid-cols-2 gap-10 sm:grid-cols-4 sm:gap-8">
          {STATS.map((s, i) => (
            <StatItem key={s.label} {...s} delay={i * 100} />
          ))}
        </div>
      </div>
    </section>
  );
}
