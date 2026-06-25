import { useState, useEffect } from "react";
import { cn } from "../../lib/utils";

interface AgoraIconProps extends React.ComponentProps<"span"> {
  /**
   * If true, play a one-time entrance spin animation.
   */
  animate?: boolean;
  /**
   * If true, disable hover spin animation.
   */
  noSpin?: boolean;
  /**
   * If true, show a border around the icon.
   */
  bordered?: boolean;
  /**
   * Size of the bordered icon: "sm" (default), "md", "lg"
   */
  size?: "sm" | "md" | "lg";
}

const borderedSizes = {
  sm: { wrapper: "p-1.5", icon: "size-3.5" },
  md: { wrapper: "p-2", icon: "size-4" },
  lg: { wrapper: "p-2.5", icon: "size-5" },
};

/** Brand accent on the shared-center node (ultramarine #2347E8). */
const ACCENT = "#2347E8";

/**
 * Agora "assembly" mark — a ring of six participants gathered around a shared
 * center: three filled nodes (people) and three outlined nodes (agents), with
 * the center node in the brand accent (the shared work). The ring uses
 * currentColor, so it inherits the surrounding text color and adapts to
 * light/dark automatically. Replaces the old 8-point aperture star, which read
 * too close to a generic AI "sparkle".
 */
function AssemblyMark() {
  return (
    <svg
      viewBox="0 0 96 96"
      className="block size-full"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="48" cy="48" r="28" fill="none" stroke="currentColor" strokeWidth="1.4" opacity={0.28} />
      <circle cx="48" cy="20" r="8" fill="currentColor" />
      <circle cx="72.2" cy="62" r="8" fill="currentColor" />
      <circle cx="23.8" cy="62" r="8" fill="currentColor" />
      <circle cx="72.2" cy="34" r="6.5" stroke="currentColor" strokeWidth="3" />
      <circle cx="48" cy="76" r="6.5" stroke="currentColor" strokeWidth="3" />
      <circle cx="23.8" cy="34" r="6.5" stroke="currentColor" strokeWidth="3" />
      <circle cx="48" cy="48" r="10" fill={ACCENT} />
    </svg>
  );
}

export function AgoraIcon({
  className,
  animate = false,
  noSpin = false,
  bordered = false,
  size = "sm",
  ...props
}: AgoraIconProps) {
  const [entranceDone, setEntranceDone] = useState(!animate);

  useEffect(() => {
    if (!animate) return;
    const timer = setTimeout(() => setEntranceDone(true), 600);
    return () => clearTimeout(timer);
  }, [animate]);

  if (bordered) {
    const sizeConfig = borderedSizes[size];
    return (
      <span
        className={cn(
          "inline-flex items-center justify-center border border-border rounded-md",
          sizeConfig.wrapper,
          className
        )}
        aria-hidden="true"
        {...props}
      >
        <span
          className={cn(
            "block",
            sizeConfig.icon,
            !entranceDone && "animate-entrance-spin",
            entranceDone && !noSpin && "hover:animate-spin"
          )}
        >
          <AssemblyMark />
        </span>
      </span>
    );
  }

  return (
    <span
      className={cn(
        "inline-block size-[1em]",
        !entranceDone && "animate-entrance-spin",
        entranceDone && !noSpin && "hover:animate-spin",
        className
      )}
      aria-hidden="true"
      {...props}
    >
      <AssemblyMark />
    </span>
  );
}
