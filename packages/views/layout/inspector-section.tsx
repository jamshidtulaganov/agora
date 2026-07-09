"use client";

import { useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";

export interface InspectorSectionProps {
  title: ReactNode;
  /** Initial open state. Uncontrolled — open state is owned internally. Default false. */
  defaultOpen?: boolean;
  children: ReactNode;
  /**
   * Optional right-aligned node in the toggle row (e.g. a status badge).
   * Rendered between the title and the chevron; clicks inside it don't
   * bubble into the toggle button.
   */
  actions?: ReactNode;
}

/**
 * Collapsible inspector-rail section: a `<button>` toggle row (title +
 * chevron) above conditionally-rendered `children`. Extracted from
 * issue-detail.tsx's ~5 hand-rolled sidebar sections (Properties, Parent
 * issue, Pull requests, Details, Token usage), which shared this exact
 * markup — see docs/sdlc-stage-cockpit-plan.md, section 1.
 */
export function InspectorSection({ title, defaultOpen = false, children, actions }: InspectorSectionProps) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div>
      <button
        type="button"
        className={cn(
          "flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70",
          !open && "text-muted-foreground hover:text-foreground",
        )}
        onClick={() => setOpen((o) => !o)}
      >
        {title}
        {actions && (
          <span className="ml-auto flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            {actions}
          </span>
        )}
        <ChevronRight
          className={cn(
            "!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
      </button>
      {open && children}
    </div>
  );
}
