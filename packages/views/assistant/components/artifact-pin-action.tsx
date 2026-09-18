"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Pin, PinOff } from "lucide-react";
import { toast } from "sonner";
import { useCurrentWorkspace } from "@agora/core/paths";
import { projectListOptions } from "@agora/core/projects/queries";
import {
  usePinArtifact,
  useUnpinArtifact,
  useSetReportSchedule,
  useDeleteReportSchedule,
} from "@agora/core/reports";
import type { ReportScheduleFrequency } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import {
  NativeSelect,
  NativeSelectOption,
} from "@agora/ui/components/ui/native-select";
import { TimeInput } from "@agora/ui/components/ui/time-input";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { ProjectPicker } from "../../projects/components/project-picker";
import { useT, useWeekdayNames } from "../../i18n";

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

          {pin && (
            <ScheduleBlock
              // A new pin is a new schedule surface — keyed so the row never
              // shows the previous pin's saved cadence.
              key={pin.id}
              wsId={wsId}
              artifactId={artifactId}
              pinId={pin.id}
              projectId={pin.projectId}
            />
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

// --- Refresh schedule ----------------------------------------------------
// docs/assistant-domain-plan.md Phase 2b. Presets only, no cron: the tightest
// cadence this can express is once a day, which is the guardrail that keeps a
// standing report from becoming standing spend.

/** The contract's presets plus the local "off" sentinel (= delete / none). */
const SCHEDULE_OPTIONS = ["off", "daily", "weekdays", "weekly"] as const;
type ScheduleOption = (typeof SCHEDULE_OPTIONS)[number];

/** Nine in the morning — the hour a standing report is actually read. */
const DEFAULT_TIME = "09:00";
/** Monday, in the 0=Sunday numbering the contract uses. */
const DEFAULT_WEEKDAY = 1;

/**
 * The viewer's own zone, detected and SHOWN — never asked. A schedule set at
 * 09:00 means 09:00 where the owner sits; making them pick that from a list of
 * ~600 IANA zones would be a question with one right answer.
 */
function detectTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

interface SavedSchedule {
  option: ScheduleOption;
  time: string;
  weekday: number;
}

/**
 * One row inside the pinned state of the dialog: cadence, time, and (weekly
 * only) a day. No card, no second dialog — a schedule is a property of the pin
 * the user is already looking at.
 *
 * It renders only for a pin this session created, because that is the only pin
 * id the 2a flow knows (there is no read-pins-for-artifact endpoint). That
 * also means it starts from "Off" rather than from a schedule it can't read:
 * the saved baseline below tracks only what THIS dialog wrote.
 */
function ScheduleBlock({
  wsId,
  artifactId,
  pinId,
  projectId,
}: {
  wsId: string;
  artifactId: string;
  pinId: string;
  projectId: string;
}) {
  const { t } = useT("assistant");
  const dayNames = useWeekdayNames("long");
  const [option, setOption] = useState<ScheduleOption>("off");
  const [time, setTime] = useState(DEFAULT_TIME);
  const [weekday, setWeekday] = useState(DEFAULT_WEEKDAY);
  const [saved, setSaved] = useState<SavedSchedule>({
    option: "off",
    time: DEFAULT_TIME,
    weekday: DEFAULT_WEEKDAY,
  });

  const setSchedule = useSetReportSchedule(wsId);
  const deleteSchedule = useDeleteReportSchedule(wsId);
  const timezone = detectTimezone();

  const dirty =
    option !== saved.option ||
    (option !== "off" && time !== saved.time) ||
    (option === "weekly" && weekday !== saved.weekday);
  const pending = setSchedule.isPending || deleteSchedule.isPending;

  const handleSave = () => {
    const next: SavedSchedule = { option, time, weekday };
    if (option === "off") {
      deleteSchedule.mutate(
        { artifactId, pinId, projectId },
        {
          onSuccess: () => {
            setSaved(next);
            toast.success(t(($) => $.artifact.pin.schedule.toast_removed));
          },
          onError: () => toast.error(t(($) => $.artifact.pin.schedule.toast_failed)),
        },
      );
      return;
    }
    setSchedule.mutate(
      {
        artifactId,
        pinId,
        projectId,
        schedule: {
          frequency: option as ReportScheduleFrequency,
          time,
          // Weekday only travels for weekly — the other presets have no day.
          ...(option === "weekly" ? { weekday } : {}),
          timezone,
        },
      },
      {
        onSuccess: () => {
          setSaved(next);
          toast.success(t(($) => $.artifact.pin.schedule.toast_saved));
        },
        onError: () => toast.error(t(($) => $.artifact.pin.schedule.toast_failed)),
      },
    );
  };

  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="shrink-0 text-xs text-muted-foreground">
          {t(($) => $.artifact.pin.schedule.label)}
        </span>
        <NativeSelect
          size="sm"
          aria-label={t(($) => $.artifact.pin.schedule.frequency_label)}
          value={option}
          onChange={(e) => setOption(e.target.value as ScheduleOption)}
        >
          {SCHEDULE_OPTIONS.map((value) => (
            <NativeSelectOption key={value} value={value}>
              {t(($) => $.artifact.pin.schedule.option[value])}
            </NativeSelectOption>
          ))}
        </NativeSelect>

        {option !== "off" && (
          <TimeInput value={time} onChange={setTime} className="h-7" />
        )}

        {option === "weekly" && (
          <NativeSelect
            size="sm"
            aria-label={t(($) => $.artifact.pin.schedule.weekday_label)}
            value={String(weekday)}
            onChange={(e) => setWeekday(parseInt(e.target.value, 10))}
          >
            {dayNames.map((name, day) => (
              <NativeSelectOption key={name} value={String(day)}>
                {name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        )}

        {dirty && (
          <Button size="sm" variant="secondary" onClick={handleSave} disabled={pending}>
            {t(($) => $.artifact.pin.schedule.save)}
          </Button>
        )}
      </div>

      {option !== "off" && (
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.artifact.pin.schedule.timezone_note, { timezone })}
        </p>
      )}
    </div>
  );
}
