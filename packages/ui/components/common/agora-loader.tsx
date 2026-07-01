import { cn } from "../../lib/utils";

/** Brand accent on the shared-center node (ultramarine #2347E8). */
const ACCENT = "#2347E8";

/**
 * Six ring participants in clockwise order from the top, each tagged with the
 * stagger delay that makes the pulse travel around the circle. `filled` marks
 * the three "people" nodes (solid) vs the three outlined "agent" nodes.
 */
const NODES = [
  { cx: 48, cy: 20, r: 8, filled: true },
  { cx: 72.2, cy: 34, r: 6.5, filled: false },
  { cx: 72.2, cy: 62, r: 8, filled: true },
  { cx: 48, cy: 76, r: 6.5, filled: false },
  { cx: 23.8, cy: 62, r: 8, filled: true },
  { cx: 23.8, cy: 34, r: 6.5, filled: false },
];

interface AgoraLoaderProps extends React.ComponentProps<"span"> {
  /** Pixel size of the animated mark. Defaults to 80. */
  size?: number;
}

/**
 * Animated Agora assembly mark for full-screen / inline loading states. Same
 * geometry as <AgoraIcon /> but in motion: the ring orbits, each participant
 * node pulses in sequence (a highlight travelling the circle), and the
 * shared-center node breathes. The mark uses currentColor for the ring nodes,
 * so it adapts to light/dark; size comes from the `size` prop (px).
 */
export function AgoraLoader({ className, size = 80, ...props }: AgoraLoaderProps) {
  return (
    <span
      className={cn("inline-block", className)}
      style={{ width: size, height: size }}
      role="status"
      aria-label="Loading"
      {...props}
    >
      <svg viewBox="0 0 96 96" className="block size-full" fill="none" aria-hidden="true">
        <g className="agora-loader-orbit">
          <circle
            className="agora-loader-ring"
            cx="48"
            cy="48"
            r="28"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
          />
          {NODES.map((n, i) => (
            <circle
              key={i}
              className="agora-loader-node"
              cx={n.cx}
              cy={n.cy}
              r={n.r}
              fill={n.filled ? "currentColor" : "none"}
              stroke={n.filled ? undefined : "currentColor"}
              strokeWidth={n.filled ? undefined : 3}
              style={{ animationDelay: `${i * 0.18}s` }}
            />
          ))}
        </g>
        <circle className="agora-loader-core" cx="48" cy="48" r="10" fill={ACCENT} />
      </svg>
    </span>
  );
}
