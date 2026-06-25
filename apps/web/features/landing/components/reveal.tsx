"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@agora/ui/lib/utils";

type RevealProps = {
  children: ReactNode;
  className?: string;
  /** Stagger delay in ms applied once the element enters the viewport. */
  delay?: number;
  /** Entrance direction. */
  from?: "up" | "down" | "none";
  /** Re-run the animation every time it scrolls back into view. */
  once?: boolean;
};

/**
 * Apple-style scroll reveal: fades and slides an element up as it enters the
 * viewport. Pure IntersectionObserver + CSS — no animation deps.
 * Falls back to fully visible when IO is unavailable (SSR) or the user
 * prefers reduced motion.
 */
export function Reveal({
  children,
  className,
  delay = 0,
  from = "up",
  once = true,
}: RevealProps) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [shown, setShown] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    if (typeof IntersectionObserver === "undefined") {
      setShown(true);
      return;
    }
    const prefersReduced = window.matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    if (prefersReduced) {
      setShown(true);
      return;
    }

    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setShown(true);
            if (once) io.disconnect();
          } else if (!once) {
            setShown(false);
          }
        }
      },
      { threshold: 0.12, rootMargin: "0px 0px -8% 0px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [once]);

  const hidden =
    from === "none"
      ? "opacity-0"
      : from === "down"
        ? "opacity-0 -translate-y-6"
        : "opacity-0 translate-y-7";

  return (
    <div
      ref={ref}
      style={{ transitionDelay: shown ? `${delay}ms` : "0ms" }}
      className={cn(
        "transition-all duration-[850ms] ease-[cubic-bezier(0.16,1,0.3,1)] will-change-transform motion-reduce:transition-none",
        shown ? "translate-y-0 opacity-100" : hidden,
        className,
      )}
    >
      {children}
    </div>
  );
}
