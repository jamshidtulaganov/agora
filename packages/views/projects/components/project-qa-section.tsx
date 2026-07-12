/* eslint-disable i18next/no-literal-string -- project admin panel; i18n follow-up */
"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { useUpdateProject } from "@agora/core/projects/mutations";
import { remoteBoxesOptions, useBindRemoteBox } from "@agora/core/runtimes";
import { useConfigStore } from "@agora/core/config";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ProjectSettings } from "@agora/core/types";

// Project QA-smoke configuration section.
//
// Surfaces the project.settings keys the QA flows read — qa_smoke_cmd (how to
// bring the app up), qa_smoke_url (where it serves), and qa_test_cmd (how to
// run the test suite, consumed by the co-code Tests pane) — so a human
// configures them from the UI instead of hand-editing the settings JSON.
// Empty values clear the override (the flows fall back to auto-detect). Saves
// merge into the existing settings blob so unrelated keys (sprint_mode, etc.)
// round-trip untouched.
export function ProjectQASection({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const updateProject = useUpdateProject();
  const [open, setOpen] = useState(false);

  // QA box binding (opt-in). Gated behind the same flag RemoteBoxesSection uses
  // — the whole control hides when the feature is off (the list also falls back
  // to [] server-side when disabled, so this is belt-and-suspenders).
  const remoteBoxesEnabled = useConfigStore((s) => s.remoteBoxesEnabled);
  const { data: boxes = [] } = useQuery({
    ...remoteBoxesOptions(wsId),
    enabled: remoteBoxesEnabled,
  });
  const bindBox = useBindRemoteBox(wsId);
  const boundBox = boxes.find((b) => b.project_id === projectId) ?? null;

  const handleBindChange = async (boxId: string) => {
    try {
      if (boxId === "") {
        // Unbind: clear the currently-bound box (empty project_id unbinds).
        if (boundBox) await bindBox.mutateAsync({ id: boundBox.id, projectId: "" });
        return;
      }
      await bindBox.mutateAsync({ id: boxId, projectId });
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to set QA box",
      );
    }
  };

  const settings = project?.settings;
  const savedCmd = (settings?.qa_smoke_cmd ?? "") as string;
  const savedUrl = (settings?.qa_smoke_url ?? "") as string;
  const savedTestCmd = (settings?.qa_test_cmd ?? "") as string;
  const savedDocsRepo = (settings?.docs_repo ?? "") as string;

  const [cmd, setCmd] = useState(savedCmd);
  const [url, setUrl] = useState(savedUrl);
  const [testCmd, setTestCmd] = useState(savedTestCmd);
  const [docsRepo, setDocsRepo] = useState(savedDocsRepo);

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

  return (
    <div>
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
      {open && (
        <div className="pl-2 space-y-2">
          <p className="text-[10px] text-muted-foreground">
            How the QA gate brings the app up and where it smokes it. Leave blank
            to let the gate auto-detect.
          </p>
          <label className="block space-y-1">
            <span className="text-[10px] font-medium text-muted-foreground">
              Start command
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
              Test command
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
          {remoteBoxesEnabled && (
            <label className="block space-y-1 border-t pt-2">
              <span className="text-[10px] font-medium text-muted-foreground">
                QA box
              </span>
              <select
                value={boundBox?.id ?? ""}
                onChange={(e) => void handleBindChange(e.target.value)}
                disabled={bindBox.isPending}
                className="h-7 w-full rounded-md border bg-transparent px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50"
              >
                <option value="">No QA box (deploy-qa off)</option>
                {boxes.map((box) => (
                  <option key={box.id} value={box.id}>
                    {box.label} ({box.ssh_host})
                  </option>
                ))}
              </select>
              <p className="text-[10px] text-muted-foreground">
                Remote box that serves this project&apos;s branches for QA.
                Issues in this project can deploy their branch here from the
                editor.
              </p>
            </label>
          )}
        </div>
      )}
    </div>
  );
}
