"use client";

import { useState } from "react";
import { ChevronRight, Settings2 } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { ProjectExecutionSection } from "./project-execution-section";

// Advanced agent behavior belongs behind one project-level entry point. The
// workflow controls stay available without exposing every optional subsystem
// as a first-class project property. Design context is generated and reviewed
// through explicit agent workflows, never edited as project configuration.
export function ProjectAgentSetupSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const [open, setOpen] = useState(false);

  return (
    <div>
      <button
        type="button"
        className={cn(
          "mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70",
          !open && "text-muted-foreground hover:text-foreground",
        )}
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <Settings2 className="!size-3 shrink-0 text-muted-foreground" />
        {t(($) => $.agent_setup.title)}
        <ChevronRight
          className={cn(
            "!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
      </button>

      {open && (
        <div className="pl-2">
          <p className="mb-2 px-2 text-[10px] leading-relaxed text-muted-foreground">
            {t(($) => $.agent_setup.description)}
          </p>
          <ProjectExecutionSection projectId={projectId} />
        </div>
      )}
    </div>
  );
}
