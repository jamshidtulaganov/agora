/* eslint-disable i18next/no-literal-string -- project admin panel; i18n follow-up */
"use client";

import { useQuery } from "@tanstack/react-query";
import { Loader2, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { useSyncBitrixProject } from "@agora/core/bitrix";
import { useWorkspaceId } from "@agora/core/hooks";
import { Button } from "@agora/ui/components/ui/button";

// Bitrix sync section. Shown only for a Bitrix-linked project (its description
// carries the durable "bitrix_group:<id>" marker, or it has been synced before).
// Surfaces the last sync time + a manual "Sync Bitrix" button that re-pulls the
// project's workgroup tasks (new + changed) on demand.
export function ProjectBitrixSection({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const sync = useSyncBitrixProject(projectId);

  const lastSynced = project?.settings?.bitrix_synced_at;
  const isBitrixLinked =
    (project?.description?.includes("bitrix_group:") ?? false) || !!lastSynced;
  if (!project || !isBitrixLinked) return null;

  const handleSync = async () => {
    if (sync.isPending) return;
    try {
      await sync.mutateAsync();
      toast.success("Bitrix sync started — updates will stream in");
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Bitrix sync failed",
      );
    }
  };

  return (
    <div className="space-y-2 border-t pt-3">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="text-xs font-medium text-muted-foreground">Bitrix</div>
          <div className="text-[11px] text-muted-foreground">
            {lastSynced
              ? `Last synced ${new Date(lastSynced).toLocaleString()}`
              : "Not synced yet"}
          </div>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-7 shrink-0 gap-1.5 px-2 text-xs"
          disabled={sync.isPending}
          onClick={() => void handleSync()}
        >
          {sync.isPending ? (
            <Loader2 className="size-3 animate-spin" />
          ) : (
            <RefreshCw className="size-3" />
          )}
          Sync Bitrix
        </Button>
      </div>
    </div>
  );
}
