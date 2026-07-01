"use client";

import { useEffect, useState } from "react";
import { MoonIcon, SunIcon } from "lucide-react";
import { useTheme } from "./theme-provider";
import { cn } from "../../lib/utils";

/**
 * Compact dark/light toggle. Flips between explicit light and dark based on the
 * currently resolved theme (so a "system" user's first click lands on the
 * opposite of what they're actually seeing). Render-guarded with `mounted` to
 * avoid a hydration mismatch: the server can't know the resolved theme, so the
 * icon is only chosen after the client mounts.
 */
export function ThemeToggle({
  className,
  ...props
}: React.ComponentProps<"button">) {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  useEffect(() => setMounted(true), []);

  const isDark = resolvedTheme === "dark";

  return (
    <button
      type="button"
      aria-label={isDark ? "Switch to light theme" : "Switch to dark theme"}
      onClick={() => setTheme(isDark ? "light" : "dark")}
      className={cn(
        "inline-flex size-9 items-center justify-center rounded-md border border-border bg-background/70 text-foreground backdrop-blur-sm transition-colors hover:bg-accent hover:text-accent-foreground",
        className,
      )}
      {...props}
    >
      {mounted ? (
        isDark ? (
          <SunIcon className="size-4" />
        ) : (
          <MoonIcon className="size-4" />
        )
      ) : (
        <MoonIcon className="size-4 opacity-0" />
      )}
    </button>
  );
}
