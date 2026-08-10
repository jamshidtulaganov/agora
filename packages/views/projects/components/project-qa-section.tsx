/* eslint-disable i18next/no-literal-string -- project admin panel; i18n follow-up */
"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { useUpdateProject } from "@agora/core/projects/mutations";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ProjectPreviewTarget, ProjectSettings } from "@agora/core/types";

const EMPTY_PREVIEW_TARGETS: ProjectPreviewTarget[] = [];

// Project QA-smoke configuration section.
//
// Surfaces the project.settings keys the QA flows read — qa_smoke_cmd (how to
// bring the app up), qa_smoke_url (where it serves), and qa_test_cmd (how to
// run the test suite, consumed by the issue Checks surface) — so a human
// configures them from the UI instead of hand-editing the settings JSON.
// Empty values clear the override (the flows fall back to auto-detect). Saves
// merge into the existing settings blob so unrelated keys (sprint_mode, etc.)
// round-trip untouched.
export function ProjectQASection({
  projectId,
  embedded = false,
}: {
  projectId: string;
  embedded?: boolean;
}) {
  const wsId = useWorkspaceId();
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const updateProject = useUpdateProject();
  const [open, setOpen] = useState(embedded);

  const settings = project?.settings;
  const savedCmd = (settings?.qa_smoke_cmd ?? "") as string;
  const savedUrl = (settings?.qa_smoke_url ?? "") as string;
  const savedTestCmd = (settings?.qa_test_cmd ?? "") as string;
  const savedDocsRepo = (settings?.docs_repo ?? "") as string;
  const savedTargets = (settings?.preview_targets ?? EMPTY_PREVIEW_TARGETS) as ProjectPreviewTarget[];

  const [cmd, setCmd] = useState(savedCmd);
  const [url, setUrl] = useState(savedUrl);
  const [testCmd, setTestCmd] = useState(savedTestCmd);
  const [docsRepo, setDocsRepo] = useState(savedDocsRepo);
  const [targets, setTargets] = useState<ProjectPreviewTarget[]>(savedTargets);

  // Re-sync local drafts when the server value changes (e.g. another client, or
  // the initial load resolving after mount).
  useEffect(() => {
    setCmd(savedCmd);
  }, [savedCmd]);
  useEffect(() => {
    setUrl(savedUrl);
  }, [savedUrl]);
  useEffect(() => {
    setTestCmd(savedTestCmd);
  }, [savedTestCmd]);
  useEffect(() => {
    setDocsRepo(savedDocsRepo);
  }, [savedDocsRepo]);
  useEffect(() => {
    setTargets(savedTargets);
  }, [savedTargets]);

  // Persist a single key, merging into the current settings blob. Normalizes
  // whitespace-only input to "" so the backend treats it as "no override".
  const saveKey = async (
    key: "qa_smoke_cmd" | "qa_smoke_url" | "qa_test_cmd" | "docs_repo",
    value: string,
  ) => {
    if (!project) return;
    const next = value.trim();
    if ((((settings?.[key] ?? "") as string)) === next) return;
    const mergedSettings: ProjectSettings = { ...settings, [key]: next };
    try {
      await updateProject.mutateAsync({ id: projectId, settings: mergedSettings });
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to save QA settings",
      );
    }
  };

  const saveTargets = async (nextTargets: ProjectPreviewTarget[]) => {
    if (!project) return;
    const normalized = nextTargets
      .map((target) => ({
        repo: target.repo.trim(),
        working_directory: target.working_directory?.trim() || undefined,
        start_command: target.start_command?.trim() || undefined,
        test_command: target.test_command?.trim() || undefined,
      }))
      .filter((target) => target.repo !== "");
    try {
      await updateProject.mutateAsync({
        id: projectId,
        settings: { ...settings, preview_targets: normalized },
      });
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to save preview targets",
      );
    }
  };

  const updateTarget = (index: number, patch: Partial<ProjectPreviewTarget>) => {
    setTargets((current) => current.map((target, targetIndex) =>
      targetIndex === index ? { ...target, ...patch } : target,
    ));
  };

  return (
    <div>
      {!embedded && (
        <button
          type="button"
          className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
          onClick={() => setOpen(!open)}
        >
          <ShieldCheck className="!size-3 shrink-0 text-muted-foreground" />
          QA smoke
          <ChevronRight
            className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
          />
        </button>
      )}
      {open && (
        <div className={embedded ? "space-y-2" : "space-y-2 pl-2"}>
          {embedded && (
            <div className="flex items-center gap-1.5 text-[10px] font-medium text-muted-foreground">
              <ShieldCheck className="size-3" />
              Preview &amp; checks
            </div>
          )}
          <p className="text-[10px] text-muted-foreground">
            Shared by artifact Preview, Checks, and the QA gate. Leave a command
            blank to let Agora detect it from the selected repository.
          </p>
          <label className="block space-y-1">
            <span className="text-[10px] font-medium text-muted-foreground">
              Start command (Preview + QA)
            </span>
            <input
              type="text"
              value={cmd}
              onChange={(e) => setCmd(e.target.value)}
              onBlur={() => void saveKey("qa_smoke_cmd", cmd)}
              onKeyDown={(e) => {
                if (e.key === "Enter") (e.target as HTMLInputElement).blur();
              }}
              placeholder="pnpm dev"
              className="h-7 w-full rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-[10px] font-medium text-muted-foreground">
              Smoke URL
            </span>
            <input
              type="text"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onBlur={() => void saveKey("qa_smoke_url", url)}
              onKeyDown={(e) => {
                if (e.key === "Enter") (e.target as HTMLInputElement).blur();
              }}
              placeholder="http://localhost:5173"
              className="h-7 w-full rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-[10px] font-medium text-muted-foreground">
              Test command (Checks + QA)
            </span>
            <input
              type="text"
              value={testCmd}
              onChange={(e) => setTestCmd(e.target.value)}
              onBlur={() => void saveKey("qa_test_cmd", testCmd)}
              onKeyDown={(e) => {
                if (e.key === "Enter") (e.target as HTMLInputElement).blur();
              }}
              placeholder="pnpm test"
              className="h-7 w-full rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
            />
          </label>
          <div className="space-y-2 border-t pt-2">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="text-[10px] font-medium text-muted-foreground">
                  Repository / folder overrides
                </div>
                <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">
                  For multi-root projects. The name must match the repository selector in Preview.
                </p>
              </div>
              <button
                type="button"
                className="inline-flex h-6 shrink-0 items-center gap-1 rounded-md border px-1.5 text-[10px] font-medium hover:bg-muted"
                onClick={() => setTargets((current) => [...current, { repo: "" }])}
              >
                <Plus className="size-3" />
                Add
              </button>
            </div>
            {targets.map((target, index) => (
              <div key={index} className="space-y-1.5 rounded-md border bg-muted/15 p-2">
                <div className="flex items-center gap-1.5">
                  <input
                    type="text"
                    value={target.repo}
                    onChange={(event) => updateTarget(index, { repo: event.target.value })}
                    onBlur={() => void saveTargets(targets)}
                    placeholder="mytrion"
                    aria-label="Repository or folder name"
                    className="h-7 min-w-0 flex-1 rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  />
                  <button
                    type="button"
                    aria-label="Remove repository override"
                    className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                    onClick={() => {
                      const next = targets.filter((_, targetIndex) => targetIndex !== index);
                      setTargets(next);
                      void saveTargets(next);
                    }}
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                </div>
                <input
                  type="text"
                  value={target.working_directory ?? ""}
                  onChange={(event) => updateTarget(index, { working_directory: event.target.value })}
                  onBlur={() => void saveTargets(targets)}
                  placeholder="Working directory, e.g. apps/web (optional)"
                  aria-label="Working directory inside repository"
                  className="h-7 w-full rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
                <input
                  type="text"
                  value={target.start_command ?? ""}
                  onChange={(event) => updateTarget(index, { start_command: event.target.value })}
                  onBlur={() => void saveTargets(targets)}
                  placeholder="Start command, e.g. pnpm dev"
                  aria-label="Repository start command"
                  className="h-7 w-full rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
                <input
                  type="text"
                  value={target.test_command ?? ""}
                  onChange={(event) => updateTarget(index, { test_command: event.target.value })}
                  onBlur={() => void saveTargets(targets)}
                  placeholder="Test command, e.g. pnpm test"
                  aria-label="Repository test command"
                  className="h-7 w-full rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
              </div>
            ))}
          </div>
          <label className="block space-y-1 border-t pt-2">
            <span className="text-[10px] font-medium text-muted-foreground">
              Docs repo (auto-docs)
            </span>
            <input
              type="text"
              value={docsRepo}
              onChange={(e) => setDocsRepo(e.target.value)}
              onBlur={() => void saveKey("docs_repo", docsRepo)}
              onKeyDown={(e) => {
                if (e.key === "Enter") (e.target as HTMLInputElement).blur();
              }}
              placeholder="https://github.com/org/docs.git"
              className="h-7 w-full rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
            />
          </label>
        </div>
      )}
    </div>
  );
}
