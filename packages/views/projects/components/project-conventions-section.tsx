/* eslint-disable i18next/no-literal-string -- project admin panel; i18n follow-up */
"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, ScrollText, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { useUpdateProject } from "@agora/core/projects/mutations";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ProjectSettings } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";

// Project conventions section.
//
// Surfaces project.settings.conventions — the human-authored coding rules (lint,
// code style, design patterns) an existing codebase already follows. Persisted
// by merging into the settings blob (unrelated keys round-trip untouched) and
// injected into EVERY agent run on the project via the claim path (see the
// server's sliceActionProjectConventionsContext), so dev / QA / design agents
// match the house style instead of re-inventing it.
//
// "Learn from repo" spawns the lead agent to study the connected repo's config
// (.eslintrc / prettier / tsconfig / AGENTS.md / existing patterns) and draft
// the conventions as a reviewable proposal — the human owns the final text.
export function ProjectConventionsSection({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const updateProject = useUpdateProject();
  const [open, setOpen] = useState(false);
  const [learning, setLearning] = useState(false);

  const settings = project?.settings;
  const saved = (settings?.conventions ?? "") as string;
  const [draft, setDraft] = useState(saved);

  // Re-sync the draft when the server value changes (another client, the
  // "Learn from repo" capture landing, or the initial load resolving).
  useEffect(() => {
    setDraft(saved);
  }, [saved]);

  const save = async () => {
    if (!project) return;
    const next = draft.trim();
    if (saved.trim() === next) return;
    const mergedSettings: ProjectSettings = { ...settings, conventions: next };
    try {
      await updateProject.mutateAsync({ id: projectId, settings: mergedSettings });
      toast.success("Conventions saved");
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : "Failed to save conventions",
      );
    }
  };

  const handleLearn = async () => {
    setLearning(true);
    try {
      await api.learnProjectConventions(projectId);
      toast.success(
        "Studying the repo — the lead agent will propose conventions shortly.",
      );
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : "Failed to start — set an agent lead and connect a repo first.",
      );
    } finally {
      setLearning(false);
    }
  };

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        <ScrollText className="!size-3 shrink-0 text-muted-foreground" />
        Conventions
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
        />
      </button>
      {open && (
        <div className="pl-2 space-y-2">
          <p className="text-[10px] text-muted-foreground">
            Coding rules the agents must follow on this project — lint, code
            style, design patterns. Injected into every agent run. Markdown.
          </p>
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => void save()}
            placeholder={
              "- Functional React components + hooks only\n" +
              "- Tailwind design tokens, never hardcoded colors\n" +
              "- snake_case DB columns, camelCase TS\n" +
              "- Errors must be actionable, no bare throws"
            }
            rows={8}
            className="w-full resize-y rounded-md border bg-transparent px-2 py-1.5 font-mono text-xs leading-relaxed outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
          />
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-full justify-start px-2 text-xs text-muted-foreground hover:text-foreground"
            disabled={learning}
            onClick={() => void handleLearn()}
          >
            <Sparkles className="size-3" />
            {learning ? "Starting…" : "Learn from repo"}
          </Button>
        </div>
      )}
    </div>
  );
}
