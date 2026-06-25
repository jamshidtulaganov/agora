import { useState } from "react";
import { ChevronLeft, Check } from "lucide-react";
import { useCreateIssue } from "@agora/core/issues/mutations";
import {
  STATUS_CONFIG,
  BOARD_STATUSES,
  PRIORITY_CONFIG,
  PRIORITY_ORDER,
} from "@agora/core/issues/config";
import type { IssueStatus, IssuePriority } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { BottomSheet } from "../components/bottom-sheet";
import { StatusDot, PriorityBars } from "../components/issue-badges";
import { haptic } from "../telegram/sdk";

type Sheet = "status" | "priority" | null;

export function CreateIssueScreen() {
  const create = useCreateIssue();
  const { back, navigate } = useRouter();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState<IssueStatus>("todo");
  const [priority, setPriority] = useState<IssuePriority>("none");
  const [sheet, setSheet] = useState<Sheet>(null);

  const canSubmit = title.trim().length > 0 && !create.isPending;

  const submit = async () => {
    if (!canSubmit) return;
    haptic("medium");
    try {
      const issue = await create.mutateAsync({
        title: title.trim(),
        description: description.trim() || undefined,
        status,
        priority,
      });
      navigate({ name: "issue", id: issue.id });
    } catch {
      // Surface failure inline; keep the draft so the user can retry.
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-border bg-card px-2 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
        <button
          type="button"
          onClick={back}
          className="flex items-center px-1 py-1 text-sm text-muted-foreground"
        >
          <ChevronLeft className="size-5" />
        </button>
        <span className="text-sm font-semibold">New issue</span>
        <button
          type="button"
          onClick={submit}
          disabled={!canSubmit}
          className="rounded-full px-3 py-1.5 text-sm font-semibold text-[var(--brand,theme(colors.blue.600))] disabled:opacity-40"
        >
          {create.isPending ? "Saving…" : "Create"}
        </button>
      </header>

      <div className="flex-1 overflow-y-auto">
        <div className="px-4 py-3">
          <input
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Issue title"
            className="w-full bg-transparent text-base font-medium text-foreground outline-none placeholder:text-muted-foreground"
          />
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Add a description…"
            rows={4}
            className="mt-3 w-full resize-none bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
          />
        </div>

        {create.isError && (
          <div className="px-4 pb-2 text-sm text-destructive">
            Couldn’t create the issue. Try again.
          </div>
        )}

        <div className="divide-y divide-border border-y border-border">
          <button
            type="button"
            onClick={() => setSheet("status")}
            className="flex w-full items-center justify-between gap-3 bg-card px-4 py-3 text-left active:bg-accent"
          >
            <span className="text-sm text-muted-foreground">Status</span>
            <span className="flex items-center gap-1.5 text-sm font-medium">
              <StatusDot status={status} />
              {STATUS_CONFIG[status].label}
            </span>
          </button>
          <button
            type="button"
            onClick={() => setSheet("priority")}
            className="flex w-full items-center justify-between gap-3 bg-card px-4 py-3 text-left active:bg-accent"
          >
            <span className="text-sm text-muted-foreground">Priority</span>
            <span className="flex items-center gap-1.5 text-sm font-medium">
              <PriorityBars priority={priority} />
              {PRIORITY_CONFIG[priority].label}
            </span>
          </button>
        </div>
      </div>

      <BottomSheet open={sheet === "status"} onClose={() => setSheet(null)} title="Status">
        <ul className="pb-2">
          {BOARD_STATUSES.map((s: IssueStatus) => (
            <li key={s}>
              <button
                type="button"
                onClick={() => {
                  setStatus(s);
                  setSheet(null);
                }}
                className="flex w-full items-center gap-2.5 px-4 py-3 text-left text-sm active:bg-accent"
              >
                <span className="flex flex-1 items-center gap-2.5">
                  <StatusDot status={s} />
                  {STATUS_CONFIG[s].label}
                </span>
                {s === status && <Check className="size-4 text-[var(--brand,theme(colors.blue.600))]" />}
              </button>
            </li>
          ))}
        </ul>
      </BottomSheet>

      <BottomSheet open={sheet === "priority"} onClose={() => setSheet(null)} title="Priority">
        <ul className="pb-2">
          {PRIORITY_ORDER.map((p: IssuePriority) => (
            <li key={p}>
              <button
                type="button"
                onClick={() => {
                  setPriority(p);
                  setSheet(null);
                }}
                className="flex w-full items-center gap-2.5 px-4 py-3 text-left text-sm active:bg-accent"
              >
                <span className="flex flex-1 items-center gap-2.5">
                  <PriorityBars priority={p} />
                  {PRIORITY_CONFIG[p].label}
                </span>
                {p === priority && <Check className="size-4 text-[var(--brand,theme(colors.blue.600))]" />}
              </button>
            </li>
          ))}
        </ul>
      </BottomSheet>
    </div>
  );
}
