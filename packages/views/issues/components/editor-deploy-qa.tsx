"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ServerCog, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { remoteBoxesOptions, useDeployIssueQA } from "@agora/core/runtimes";
import { useConfigStore } from "@agora/core/config";

// Deploy-to-QA-box — checks the issue's branch out onto the remote box bound to
// its project (git-sync), so the box serves the branch under review and the QA
// gate (with qa_smoke_url pointed at the box) can test it. Mirrors EditorRunQA:
// a single inline action near "Run QA". The box is auto-resolved server-side
// from the issue; we only resolve "is a box bound?" client-side to decide
// whether to render at all. Rendered only when the feature flag is on AND the
// issue's project has a bound box — otherwise nothing shows.

export function EditorDeployQA({
  issueId,
  wsId,
  projectId,
}: {
  issueId: string;
  wsId: string;
  projectId: string | null;
}) {
  const remoteBoxesEnabled = useConfigStore((s) => s.remoteBoxesEnabled);
  const { data: boxes = [] } = useQuery({
    ...remoteBoxesOptions(wsId),
    enabled: remoteBoxesEnabled,
  });
  const deploy = useDeployIssueQA(wsId);
  const [branch, setBranch] = useState("");

  const boundBox = projectId
    ? (boxes.find((b) => b.project_id === projectId) ?? null)
    : null;

  // Hidden entirely unless the feature is on and the project is bound to a box.
  if (!remoteBoxesEnabled || !boundBox) return null;

  // Prefill the branch input from the box's last-synced branch on first paint.
  const branchValue = branch || boundBox.last_branch;

  const run = async () => {
    const b = branchValue.trim();
    if (!b || deploy.isPending) return;
    try {
      const res = await deploy.mutateAsync({ issueId, branch: b });
      if (res.ok) {
        toast.success(`Deployed ${res.box.ssh_host} → ${res.branch}`);
      } else {
        toast.error(
          `Deploy failed: ${res.output?.slice(0, 200) || "see box status"}`,
        );
      }
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to deploy to QA box",
      );
    }
  };

  return (
    <div className="flex items-center gap-1.5">
      <input
        type="text"
        value={branchValue}
        onChange={(e) => setBranch(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") void run();
        }}
        placeholder="branch"
        aria-label="Branch to deploy to QA box"
        className="h-5 w-28 rounded border bg-transparent px-1.5 font-mono text-[10px] outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
      />
      <button
        type="button"
        onClick={() => void run()}
        disabled={branchValue.trim() === "" || deploy.isPending}
        title={`Check this branch out onto ${boundBox.label} (${boundBox.ssh_host}) so the QA box serves it`}
        className="inline-flex items-center gap-1 text-[10px] text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
      >
        {deploy.isPending ? (
          <Loader2 className="h-3 w-3 animate-spin" />
        ) : (
          <ServerCog className="h-3 w-3" />
        )}
        Deploy to QA box
      </button>
    </div>
  );
}
