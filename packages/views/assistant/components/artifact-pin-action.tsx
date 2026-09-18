"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Pin, PinOff } from "lucide-react";
import { toast } from "sonner";
import { useCurrentWorkspace } from "@agora/core/paths";
import { projectListOptions } from "@agora/core/projects/queries";
import { usePinArtifact, useUnpinArtifact } from "@agora/core/reports";
import { Button } from "@agora/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { ProjectPicker } from "../../projects/components/project-picker";
import { useT } from "../../i18n";

/**
 * Publish action in the artifact pane header — pins the artifact to a project
 * so every member of that workspace can read its current body
 * (docs/assistant-domain-plan.md Phase 2a).
 *
 * The pane is only ever rendered for the artifact's OWNER today (the assistant
 * page and the owner's own artifact library), so this needs no extra client
 * gating; the server is the real owner check either way.
 *
 * Known-pinned state, and why it is session-local:
 * Phase 2a ships no "which pins does this artifact have" read — the contract
 * is pin / unpin / list-per-project / read-one. Rather than invent an
 * endpoint, this component remembers only the pin IT created: after a
 * successful pin the button flips to PinOff and the dialog offers Unpin. On a
 * fresh page load a pinned artifact looks unpinned again — the cost is one
 * redundant-looking Pin click, which is harmless because POST is idempotent
 * (it returns the EXISTING pin for the same artifact+project), and the
 * authoritative list lives on the project page. Trade: a slightly lossy
 * affordance instead of a speculative endpoint.
 */
export function ArtifactPinAction({ artifactId }: { artifactId: string }) {
  const { t } = useT("assistant");
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const [open, setOpen] = useState(false);
  const [projectId, setProjectId] = useState<string | null>(null);
  const [pin, setPin] = useState<{ id: string; projectId: string } | null>(null);

  // Switching the pane to a sibling artifact must not carry the previous
  // artifact's pin (or its half-made project choice) over.
  useEffect(() => {
    setPin(null);
    setProjectId(null);
    setOpen(false);
  }, [artifactId]);

  const { data: projects = [] } = useQuery({
    ...projectListOptions(wsId),
    enabled: !!wsId,
  });
  const pinArtifact = usePinArtifact(wsId);
  const unpinArtifact = useUnpinArtifact(wsId);

  // No workspace in context (a surface outside the dashboard) means nothing to
  // publish into — the action simply isn't offered.
  if (!wsId) return null;

  const projectName = (id: string) =>
    projects.find((project) => project.id === id)?.title || t(($) => $.artifact.pin.unknown_project);

  const handlePin = () => {
    if (!projectId) return;
    const target = projectId;
    pinArtifact.mutate(
      { artifactId, projectId: target },
      {
        onSuccess: (created) => {
          setOpen(false);
          // An empty id means the create response was unreadable: the pin
          // exists server-side, but this build can't address it, so it does
          // not offer an Unpin it cannot perform.
          if (created.id) setPin({ id: created.id, projectId: target });
          toast.success(t(($) => $.artifact.pin.toast_pinned, { project: projectName(target) }));
        },
        onError: () => toast.error(t(($) => $.artifact.pin.toast_failed)),
      },
    );
  };

  const handleUnpin = () => {
    if (!pin) return;
    unpinArtifact.mutate(
      { artifactId, pinId: pin.id, projectId: pin.projectId },
      {
        onSuccess: () => {
          setPin(null);
          setOpen(false);
          toast.success(t(($) => $.artifact.pin.toast_unpinned));
        },
        onError: () => toast.error(t(($) => $.artifact.pin.toast_unpin_failed)),
      },
    );
  };

  const label = pin ? t(($) => $.artifact.pin.unpin_action) : t(($) => $.artifact.pin.action);

  return (
    <>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={label}
              className="shrink-0 text-muted-foreground"
              onClick={() => setOpen(true)}
            />
          }
        >
          {pin ? <PinOff /> : <Pin />}
        </TooltipTrigger>
        <TooltipContent side="bottom">{label}</TooltipContent>
      </Tooltip>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.artifact.pin.dialog_title)}</DialogTitle>
            <DialogDescription>
              {pin
                ? t(($) => $.artifact.pin.pinned_to, { project: projectName(pin.projectId) })
                : t(($) => $.artifact.pin.visibility_note)}
            </DialogDescription>
          </DialogHeader>

          {!pin && (
            <div className="flex items-center gap-2 text-sm">
              <span className="shrink-0 text-xs text-muted-foreground">
                {t(($) => $.artifact.pin.project_label)}
              </span>
              <ProjectPicker
                projectId={projectId}
                onUpdate={(updates) => setProjectId(updates.project_id ?? null)}
              />
            </div>
          )}

          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {pin ? t(($) => $.artifact.pin.close) : t(($) => $.artifact.pin.cancel)}
            </Button>
            {pin ? (
              <Button
                variant="destructive"
                onClick={handleUnpin}
                disabled={unpinArtifact.isPending}
              >
                {t(($) => $.artifact.pin.unpin)}
              </Button>
            ) : (
              <Button onClick={handlePin} disabled={!projectId || pinArtifact.isPending}>
                {t(($) => $.artifact.pin.confirm)}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
