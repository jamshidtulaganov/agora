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

/**
 * Pure CSS 8-point aperture star icon matching the Agora logo.
 * Uses currentColor so it adapts to light/dark themes automatically.
 * Clip-path polygon traced from the brand star SVG coordinates.
 */
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

  const clipPath = `polygon(
    50% 4%, 57.27% 32.45%, 82.5% 17.5%, 67.55% 42.73%,
    96% 50%, 67.55% 57.27%, 82.5% 82.5%, 57.27% 67.55%,
    50% 96%, 42.73% 67.55%, 17.5% 82.5%, 32.45% 57.27%,
    4% 50%, 32.45% 42.73%, 17.5% 17.5%, 42.73% 32.45%
  )`;

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
          <span
            className="block size-full bg-current"
            style={{ clipPath }}
          />
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
      <span
        className="block size-full bg-current"
        style={{ clipPath }}
      />
    </span>
  );
}
