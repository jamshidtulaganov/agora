import { useCallback, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, Check, CircleSlash } from "lucide-react";
import { useCreateIssue } from "@agora/core/issues/mutations";
import { memberListOptions, agentListOptions } from "@agora/core/workspace/queries";
import { BOARD_STATUSES, PRIORITY_ORDER } from "@agora/core/issues/config";
import { useWorkspaceId } from "@agora/core/hooks";
import type { IssueStatus, IssuePriority, IssueAssigneeType } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { BottomSheet } from "../components/bottom-sheet";
import { StatusDot, PriorityBars } from "../components/issue-badges";
import { Avatar } from "../components/avatar";
import { haptic } from "../telegram/sdk";
import { useMainButton } from "../telegram/native-buttons";
import { useT } from "../i18n";

type Sheet = "status" | "priority" | "assignee" | null;

export function CreateIssueScreen() {
  const wsId = useWorkspaceId();
  const t = useT();
  const create = useCreateIssue();
  const { back, navigate } = useRouter();
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState<IssueStatus>("todo");
  const [priority, setPriority] = useState<IssuePriority>("none");
  const [assigneeType, setAssigneeType] = useState<IssueAssigneeType | null>(null);
  const [assigneeId, setAssigneeId] = useState<string | null>(null);
  const [sheet, setSheet] = useState<Sheet>(null);

  const canSubmit = title.trim().length > 0 && !create.isPending;
  const activeAgents = agents.filter((a) => !a.archived_at);

  const assigneeNode = () => {
    if (!assigneeId || !assigneeType) {
      return <span className="text-muted-foreground">{t("common.unassigned")}</span>;
    }
    if (assigneeType === "member") {
      const m = members.find((x) => x.user_id === assigneeId);
      const name = m?.name ?? t("common.member");
      return (
        <>
          <Avatar name={name} size={20} />
          {name}
        </>
      );
    }
    const a = agents.find((x) => x.id === assigneeId);
    const name = a?.name ?? t("common.agent");
    return (
      <>
        <Avatar name={name} isAgent size={20} />
        {name}
      </>
    );
  };

  const submit = useCallback(async () => {
    if (title.trim().length === 0 || create.isPending) return;
    haptic("medium");
    try {
      const issue = await create.mutateAsync({
        title: title.trim(),
        description: description.trim() || undefined,
        status,
        priority,
        ...(assigneeType && assigneeId
          ? { assignee_type: assigneeType, assignee_id: assigneeId }
          : {}),
      });
      navigate({ name: "issue", id: issue.id });
    } catch {
      // Surface failure inline; keep the draft so the user can retry.
    }
  }, [title, description, status, priority, assigneeType, assigneeId, create, navigate]);

  // Telegram's native primary CTA at the bottom of the screen.
  useMainButton({
    text: create.isPending ? t("common.saving") : t("create.title"),
    visible: true,
    enabled: canSubmit,
    onClick: submit,
  });

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center gap-1 border-b border-border bg-card px-2 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
        <button
          type="button"
          onClick={back}
          className="flex items-center px-1 py-1 text-sm text-muted-foreground"
        >
          <ChevronLeft className="size-5" />
        </button>
        <span className="text-sm font-semibold">{t("create.title")}</span>
      </header>

      <div className="flex-1 overflow-y-auto">
        <div className="px-4 py-3">
          <input
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void submit();
              }
            }}
            placeholder={t("create.issueTitle")}
            className="w-full bg-transparent text-lg font-medium text-foreground outline-none placeholder:text-muted-foreground"
          />
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder={t("create.description")}
            rows={4}
            className="mt-4 w-full resize-none bg-transparent text-[15px] leading-relaxed text-foreground outline-none placeholder:text-muted-foreground"
          />
        </div>

        {create.isError && (
          <div className="px-4 pb-2 text-sm text-destructive">
            {t("create.error")}
          </div>
        )}

        <div className="divide-y divide-border border-y border-border">
          <FieldRow label={t("create.status")} onClick={() => setSheet("status")}>
            <StatusDot status={status} />
            {t(`status.${status}`)}
          </FieldRow>
          <FieldRow label={t("create.priority")} onClick={() => setSheet("priority")}>
            <PriorityBars priority={priority} />
            {t(`priority.${priority}`)}
          </FieldRow>
          <FieldRow label={t("create.assignee")} onClick={() => setSheet("assignee")}>
            {assigneeNode()}
          </FieldRow>
        </div>
      </div>

      <BottomSheet open={sheet === "status"} onClose={() => setSheet(null)} title={t("create.status")}>
        <ul className="pb-2">
          {BOARD_STATUSES.map((s: IssueStatus) => (
            <OptionRow
              key={s}
              selected={s === status}
              onClick={() => {
                setStatus(s);
                setSheet(null);
              }}
            >
              <StatusDot status={s} />
              {t(`status.${s}`)}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>

      <BottomSheet open={sheet === "priority"} onClose={() => setSheet(null)} title={t("create.priority")}>
        <ul className="pb-2">
          {PRIORITY_ORDER.map((p: IssuePriority) => (
            <OptionRow
              key={p}
              selected={p === priority}
              onClick={() => {
                setPriority(p);
                setSheet(null);
              }}
            >
              <PriorityBars priority={p} />
              {t(`priority.${p}`)}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>

      <BottomSheet open={sheet === "assignee"} onClose={() => setSheet(null)} title={t("create.assignee")}>
        <ul className="pb-2">
          <OptionRow
            selected={!assigneeId}
            onClick={() => {
              setAssigneeType(null);
              setAssigneeId(null);
              setSheet(null);
            }}
          >
            <CircleSlash className="size-5 text-muted-foreground" />
            {t("common.unassigned")}
          </OptionRow>
          {members.map((m) => (
            <OptionRow
              key={m.user_id}
              selected={assigneeType === "member" && assigneeId === m.user_id}
              onClick={() => {
                setAssigneeType("member");
                setAssigneeId(m.user_id);
                setSheet(null);
              }}
            >
              <Avatar name={m.name} size={24} />
              {m.name}
            </OptionRow>
          ))}
          {activeAgents.map((a) => (
            <OptionRow
              key={a.id}
              selected={assigneeType === "agent" && assigneeId === a.id}
              onClick={() => {
                setAssigneeType("agent");
                setAssigneeId(a.id);
                setSheet(null);
              }}
            >
              <Avatar name={a.name} isAgent size={24} />
              {a.name}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>
    </div>
  );
}

function FieldRow({
  label,
  children,
  onClick,
}: {
  label: string;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center justify-between gap-3 bg-card px-4 py-3.5 text-left transition-colors active:bg-accent"
    >
      <span className="text-[15px] text-muted-foreground">{label}</span>
      <span className="flex items-center gap-1.5 text-[15px] font-medium text-foreground">
        {children}
      </span>
    </button>
  );
}

function OptionRow({
  children,
  selected,
  onClick,
}: {
  children: React.ReactNode;
  selected?: boolean;
  onClick: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        onClick={onClick}
        className="flex w-full items-center gap-2.5 px-4 py-3.5 text-left text-[15px] transition-colors active:bg-accent"
      >
        <span className="flex flex-1 items-center gap-2.5">{children}</span>
        {selected && <Check className="size-4 text-brand" />}
      </button>
    </li>
  );
}
