"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { projectReportsOptions, reportOptions } from "@agora/core/reports";
import { useWorkspaceId } from "@agora/core/hooks";
import { cn } from "@agora/ui/lib/utils";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { ArtifactBody } from "../../assistant/components/artifact-body";
import { artifactKindIcon } from "../../assistant/components/artifact-card";
import { useT, useTimeAgo } from "../../i18n";

/**
 * Reports pinned to this project (docs/assistant-domain-plan.md Phase 2a).
 *
 * A report is an assistant artifact its owner published here; the row always
 * reflects the artifact's CURRENT version, so re-running the recipe refreshes
 * what the team reads. Read-only by design — no edit, no version history, no
 * link back into the owner's session.
 *
 * Deliberately has NO empty state: with nothing pinned the section renders
 * nothing at all, so a project that never uses the assistant carries no
 * permanent placeholder in its sidebar.
 */
export function ProjectReportsSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const timeAgo = useTimeAgo();
  const [open, setOpen] = useState(true);
  const [openPinId, setOpenPinId] = useState<string | null>(null);

  const { data = [] } = useQuery(projectReportsOptions(wsId, projectId));
  // A row without a pin_id can't be opened (drifted response) — hide it
  // rather than render a dead click target.
  const reports = data.filter((report) => report.pin_id);

  if (reports.length === 0) return null;

  return (
    <div>
      <div className="mb-2 flex items-center justify-between gap-1">
        <button
          type="button"
          className={cn(
            "flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70",
            open ? "" : "text-muted-foreground hover:text-foreground",
          )}
          onClick={() => setOpen(!open)}
        >
          {t(($) => $.reports.section_header)}
          <ChevronRight
            className={cn(
              "!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
              open && "rotate-90",
            )}
          />
        </button>
      </div>

      {open && (
        <div className="space-y-1 pl-2">
          {reports.map((report) => {
            const Icon = artifactKindIcon(report.kind);
            const title = report.title.trim() || t(($) => $.reports.untitled);
            return (
              <button
                key={report.pin_id}
                type="button"
                onClick={() => setOpenPinId(report.pin_id)}
                aria-label={t(($) => $.reports.open_aria, { title })}
                className="flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-accent/50"
              >
                <Icon className="size-3.5 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-xs font-medium">{title}</div>
                  <div className="mt-0.5 truncate text-[11px] text-muted-foreground">
                    {t(($) => $.reports.meta, {
                      version: report.version,
                      time: timeAgo(report.updated_at),
                    })}
                    {report.owner.name && (
                      <> · {t(($) => $.reports.owner, { name: report.owner.name })}</>
                    )}
                  </div>
                </div>
              </button>
            );
          })}
        </div>
      )}

      {openPinId && (
        <ReportViewer
          key={openPinId}
          wsId={wsId}
          pinId={openPinId}
          onClose={() => setOpenPinId(null)}
        />
      )}
    </div>
  );
}

/**
 * Read-only viewer. Reuses the assistant's artifact renderers verbatim
 * (ArtifactBody) so chart / table / markdown / html behave — and downgrade —
 * exactly as they do in the owner's pane, including the sandboxed HTML frame.
 */
function ReportViewer({
  wsId,
  pinId,
  onClose,
}: {
  wsId: string;
  pinId: string;
  onClose: () => void;
}) {
  const { t } = useT("projects");
  const { data, isLoading } = useQuery(reportOptions(wsId, pinId));
  // `pin_id === ""` is the EMPTY_PINNED_REPORT fallback — a 200 whose body
  // failed schema validation. Same user-visible outcome as a 404.
  const report = data && data.pin_id ? data : null;

  return (
    <Dialog open onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="truncate pr-8">
            {report?.title.trim() || t(($) => $.reports.untitled)}
          </DialogTitle>
        </DialogHeader>
        <div className="max-h-[70vh] min-h-24 overflow-auto">
          {isLoading && (
            <p className="py-8 text-center text-sm text-muted-foreground">
              {t(($) => $.reports.loading)}
            </p>
          )}
          {!isLoading && !report && (
            <p className="py-8 text-center text-sm text-muted-foreground">
              {t(($) => $.reports.unavailable)}
            </p>
          )}
          {report && <ArtifactBody artifact={report} />}
        </div>
      </DialogContent>
    </Dialog>
  );
}
