"use client";

import { useState } from "react";
import { CalendarClock, ChevronRight, X as XIcon } from "lucide-react";
import { toast } from "sonner";
import { useCreateSprint } from "@agora/core/sprints/mutations";
import {
  SPRINT_STATUS_CONFIG,
  SPRINT_STATUS_ORDER,
} from "@agora/core/sprints/config";
import {
  toDateOnly,
  dateOnlyToLocalDate,
  formatDateOnly,
} from "@agora/core/issues/date";
import { useCurrentWorkspace } from "@agora/core/paths";
import type { SprintStatus } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { Dialog, DialogContent, DialogTitle } from "@agora/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@agora/ui/components/ui/dropdown-menu";
import { Popover, PopoverTrigger, PopoverContent } from "@agora/ui/components/ui/popover";
import { Tooltip, TooltipTrigger, TooltipContent } from "@agora/ui/components/ui/tooltip";
import { Button } from "@agora/ui/components/ui/button";
import { Calendar } from "@agora/ui/components/ui/calendar";
import { TitleEditor } from "../editor";
import { useT } from "../i18n";
import { useSprintStatusLabels } from "../projects/components/sprint-labels";

function PillButton({
  children,
  className,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs",
        "hover:bg-accent/60 transition-colors cursor-pointer",
        className,
      )}
      {...props}
    >
      {children}
    </button>
  );
}

/**
 * Mirror of {@link CreateProjectModal}, scoped to a sprint. The target project
 * is supplied via the modal store `data` ({ project_id }); without it the
 * modal can't create anything, so it renders nothing. Goal is a single-line
 * input (sprints are short-lived, lightweight objects — no rich editor like
 * the project description).
 */
export function CreateSprintModal({
  onClose,
  data,
}: {
  onClose: () => void;
  data: Record<string, unknown> | null;
}) {
  const { t } = useT("modals");
  const workspace = useCurrentWorkspace();
  const workspaceName = workspace?.name;
  const statusLabels = useSprintStatusLabels();
  const projectId = typeof data?.project_id === "string" ? data.project_id : null;

  const [name, setName] = useState("");
  const [goal, setGoal] = useState("");
  const [status, setStatus] = useState<SprintStatus>("planned");
  const [startDate, setStartDate] = useState<string | null>(null);
  const [endDate, setEndDate] = useState<string | null>(null);
  const [startOpen, setStartOpen] = useState(false);
  const [endOpen, setEndOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  // `useCreateSprint` is keyed by project — guard against a hookless render by
  // always calling it with a (possibly empty) id and gating submit on a real one.
  const createSprint = useCreateSprint(projectId ?? "");

  const handleSubmit = async () => {
    if (!name.trim() || submitting || !projectId) return;
    setSubmitting(true);
    try {
      await createSprint.mutateAsync({
        name: name.trim(),
        goal: goal.trim() || undefined,
        status,
        start_date: startDate,
        end_date: endDate,
      });
      onClose();
      toast.success(t(($) => $.create_sprint.toast_created));
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.create_sprint.toast_failed),
      );
    } finally {
      setSubmitting(false);
    }
  };

  const startLocal = dateOnlyToLocalDate(startDate);
  const endLocal = dateOnlyToLocalDate(endDate);

  if (!projectId) return null;

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent
        showCloseButton={false}
        className={cn(
          "p-0 gap-0 flex flex-col overflow-hidden",
          "!top-1/2 !left-1/2 !-translate-x-1/2 !-translate-y-1/2",
          "!max-w-2xl !w-full",
        )}
      >
        <DialogTitle className="sr-only">{t(($) => $.create_sprint.title)}</DialogTitle>

        <div className="flex items-center justify-between px-5 pt-3 pb-2 shrink-0">
          <div className="flex items-center gap-1.5 text-xs">
            <span className="text-muted-foreground">{workspaceName}</span>
            <ChevronRight className="size-3 text-muted-foreground/50" />
            <span className="font-medium">{t(($) => $.create_sprint.title_breadcrumb)}</span>
          </div>
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  onClick={onClose}
                  className="rounded-sm p-1.5 opacity-70 hover:opacity-100 hover:bg-accent/60 transition-all cursor-pointer"
                >
                  <XIcon className="size-4" />
                </button>
              }
            />
            <TooltipContent side="bottom">{t(($) => $.common.close)}</TooltipContent>
          </Tooltip>
        </div>

        <div className="px-5 pb-1 shrink-0">
          <TitleEditor
            autoFocus
            placeholder={t(($) => $.create_sprint.name_placeholder)}
            className="text-lg font-semibold"
            onChange={(v) => setName(v)}
            onSubmit={handleSubmit}
          />
        </div>

        <div className="px-5 pb-3 shrink-0">
          <input
            type="text"
            value={goal}
            onChange={(e) => setGoal(e.target.value)}
            placeholder={t(($) => $.create_sprint.goal_placeholder)}
            className="w-full bg-transparent text-sm placeholder:text-muted-foreground outline-none"
          />
        </div>

        {/* Footer: status + date pills (left) + Create (right) — mirrors create-project. */}
        <div className="flex items-center justify-between gap-2 px-4 py-3 border-t shrink-0">
          <div className="flex items-center gap-1.5 flex-wrap min-w-0">
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <PillButton>
                    <span className={cn("size-2 rounded-full", SPRINT_STATUS_CONFIG[status].dotColor)} />
                    <span>{statusLabels[status]}</span>
                  </PillButton>
                }
              />
              <DropdownMenuContent align="start" className="w-44">
                {SPRINT_STATUS_ORDER.map((s) => (
                  <DropdownMenuItem key={s} onClick={() => setStatus(s)}>
                    <span className={cn("size-2 rounded-full", SPRINT_STATUS_CONFIG[s].dotColor)} />
                    <span>{statusLabels[s]}</span>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>

            <Popover open={startOpen} onOpenChange={setStartOpen}>
              <PopoverTrigger
                render={
                  <PillButton>
                    <CalendarClock className="size-3" />
                    <span className={startLocal ? "" : "text-muted-foreground"}>
                      {startLocal
                        ? formatDateOnly(startDate, { month: "short", day: "numeric" }, "en-US")
                        : t(($) => $.create_sprint.start_date_label)}
                    </span>
                  </PillButton>
                }
              />
              <PopoverContent className="w-auto p-0" align="start">
                <Calendar
                  mode="single"
                  selected={startLocal}
                  onSelect={(d: Date | undefined) => {
                    setStartDate(d ? toDateOnly(d) : null);
                    setStartOpen(false);
                  }}
                />
                {startLocal && (
                  <div className="border-t px-3 py-2">
                    <Button
                      variant="ghost"
                      size="xs"
                      onClick={() => { setStartDate(null); setStartOpen(false); }}
                      className="text-muted-foreground hover:text-foreground"
                    >
                      {t(($) => $.create_sprint.clear_date)}
                    </Button>
                  </div>
                )}
              </PopoverContent>
            </Popover>

            <Popover open={endOpen} onOpenChange={setEndOpen}>
              <PopoverTrigger
                render={
                  <PillButton>
                    <CalendarClock className="size-3" />
                    <span className={endLocal ? "" : "text-muted-foreground"}>
                      {endLocal
                        ? formatDateOnly(endDate, { month: "short", day: "numeric" }, "en-US")
                        : t(($) => $.create_sprint.end_date_label)}
                    </span>
                  </PillButton>
                }
              />
              <PopoverContent className="w-auto p-0" align="start">
                <Calendar
                  mode="single"
                  selected={endLocal}
                  onSelect={(d: Date | undefined) => {
                    setEndDate(d ? toDateOnly(d) : null);
                    setEndOpen(false);
                  }}
                />
                {endLocal && (
                  <div className="border-t px-3 py-2">
                    <Button
                      variant="ghost"
                      size="xs"
                      onClick={() => { setEndDate(null); setEndOpen(false); }}
                      className="text-muted-foreground hover:text-foreground"
                    >
                      {t(($) => $.create_sprint.clear_date)}
                    </Button>
                  </div>
                )}
              </PopoverContent>
            </Popover>
          </div>

          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={!name.trim() || submitting}
            className="shrink-0"
          >
            {submitting ? t(($) => $.create_sprint.submitting) : t(($) => $.create_sprint.submit)}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
