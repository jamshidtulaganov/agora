import type { ReactNode } from "react"

import { cn } from "@agora/ui/lib/utils"

// Radial readiness ring — an SVG stroke-dasharray arc that fills to `value`.
// The release cockpit's readiness gestalt: a single glance says how close a
// sprint is to shipping, tinted by state. Distinct from the tiny sub-task
// ring in @agora/views/issues (done/total, no tone/center-slot/a11y) — this
// one is a labelled, tone-driven gauge with a center content slot.
//
// Color vocabulary matches the codebase norm (emerald = ready, amber = warn,
// muted = far, destructive = blocked). The track + indicator both draw with
// `currentColor` (the tone text color) — the track is dimmed via strokeOpacity
// — so no dependency on Tailwind's semantic `stroke-*` utilities.

export type ProgressRingTone = "ready" | "close" | "far" | "blocked"

const TONE_TEXT: Record<ProgressRingTone, string> = {
  ready: "text-emerald-500",
  close: "text-amber-500",
  far: "text-muted-foreground",
  blocked: "text-destructive",
}

export function ProgressRing({
  value,
  size = 48,
  strokeWidth = 4,
  tone = "far",
  className,
  children,
  "aria-label": ariaLabel,
}: {
  /** Fill fraction, 0..1. Non-finite / out-of-range values are clamped. */
  value: number
  size?: number
  strokeWidth?: number
  tone?: ProgressRingTone
  className?: string
  /** Center content (e.g. "8/10"). Rendered in foreground color, not the tone. */
  children?: ReactNode
  "aria-label"?: string
}) {
  const clamped = Math.max(0, Math.min(1, Number.isFinite(value) ? value : 0))
  const radius = (size - strokeWidth) / 2
  const circumference = 2 * Math.PI * radius
  const offset = circumference * (1 - clamped)
  const label = ariaLabel ?? `${Math.round(clamped * 100)}%`

  return (
    <div
      role="img"
      aria-label={label}
      className={cn(
        "relative inline-flex shrink-0 items-center justify-center",
        TONE_TEXT[tone],
        className,
      )}
      style={{ width: size, height: size }}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        className="-rotate-90"
        aria-hidden
      >
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          stroke="currentColor"
          strokeOpacity={0.15}
          strokeWidth={strokeWidth}
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          stroke="currentColor"
          strokeWidth={strokeWidth}
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={offset}
          className="transition-[stroke-dashoffset] duration-700 ease-out motion-reduce:transition-none"
        />
      </svg>
      {children != null && (
        <div className="absolute inset-0 flex flex-col items-center justify-center leading-none text-foreground">
          {children}
        </div>
      )}
    </div>
  )
}
