"use client";

import { CheckCircle2, Circle, Loader2 } from "lucide-react";
import type { Issue } from "@agora/core/types";
import { useActorName } from "@agora/core/workspace/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { ActorAvatar } from "../../common/actor-avatar";
import { useTaskLive } from "./stage-live-process";
import { QALiveCaseChildren } from "./qa-live-cases";

// The Plan panel — the work as a set of FIXED, meaningful parts, live, on the
// issue detail page. Two shapes, one look:
//   • Orchestrated issue (has sub-issues) → each PART is a real sub-issue: its
//     status, and which agent owns it, with the active part highlighted.
//   • Solo issue (no sub-issues) → the single running agent's own deliverable
//     checklist (the ```todo plan, kept to real deliverables by the runtime
//     brief — not mechanical read/checkout/commit steps).
// The moment-to-moment "what's happening now" churn lives in the SDLC stepper's
// live line, NOT here — this panel stays the stable plan.

type PartState = "done" | "doing" | "pending";

// A sub-issue's plan state. in_progress AND in_review are both "doing" — the
// part is still being worked (dev done, QA/review running). done/cancelled are
// settled; everything else is pending.
function issuePartState(status: string): PartState {
  if (status === "done" || status === "cancelled") return "done";
  if (status === "in_progress" || status === "in_review") return "doing";
  return "pending";
}

function StateIcon({ state }: { state: PartState }) {
  if (state === "done")
    return <CheckCircle2 aria-hidden className="size-3.5 shrink-0 text-success" />;
  if (state === "doing")
    return <Loader2 aria-hidden className="size-3.5 shrink-0 animate-spin text-info" />;
  return <Circle aria-hidden className="size-3.5 shrink-0 text-muted-foreground/40" />;
}

function labelClass(state: PartState): string {
  return cn(
    "min-w-0 flex-1 truncate",
    state === "done" && "text-muted-foreground/60 line-through",
    state === "doing" && "font-medium text-foreground",
    state === "pending" && "text-muted-foreground",
  );
}

const rowClass = (state: PartState) =>
  cn(
    "flex items-center gap-2 rounded-md px-2 py-1.5 text-xs transition-colors",
    state === "doing" && "bg-info/10",
  );

// Shared shell: the "Plan" header (owning agent + progress) over a row list.
function PanelShell({
  agentId,
  done,
  total,
  children,
}: {
  agentId: string | null | undefined;
  done: number;
  total: number;
  children: React.ReactNode;
}) {
  const { t } = useT("issues");
  return (
    <section
      className="rounded-lg border bg-muted/20 px-3 py-2.5"
      aria-label={t(($) => $.plan_panel.title)}
    >
      <div className="mb-1.5 flex items-center justify-between gap-2 text-[11px] font-medium text-muted-foreground">
        <span className="flex items-center gap-1.5">
          {agentId && <ActorAvatar actorType="agent" actorId={agentId} size={14} />}
          {t(($) => $.plan_panel.title)}
        </span>
        <span className="font-mono tabular-nums">
          {t(($) => $.plan_panel.progress, { done, total })}
        </span>
      </div>
      <ul className="flex flex-col gap-0.5">{children}</ul>
    </section>
  );
}

// Orchestrated part = a sub-issue, linking through, showing its owner agent.
function PartRow({ child }: { child: Issue }) {
  const paths = useWorkspacePaths();
  const { getActorName } = useActorName();
  const state = issuePartState(child.status);
  const hasOwner = !!(child.assignee_type && child.assignee_id);

  return (
    <li>
      <AppLink
        href={paths.issueDetail(child.id)}
        aria-current={state === "doing" || undefined}
        className={cn(rowClass(state), "hover:bg-accent/50")}
      >
        <StateIcon state={state} />
        <span className={labelClass(state)}>{child.title}</span>
        {hasOwner && (
          <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
            <ActorAvatar
              actorType={child.assignee_type!}
              actorId={child.assignee_id!}
              size={16}
              showStatusDot={state === "doing"}
            />
            <span className="hidden max-w-[7rem] truncate sm:inline">
              {getActorName(child.assignee_type!, child.assignee_id!)}
            </span>
          </span>
        )}
      </AppLink>
    </li>
  );
}

// Solo plan = the running agent's own deliverable checklist. During the QA
// stage, the issue's test-case runs nest as CHILDREN of the in-progress step —
// the plan item says "run the tests", its children show WHICH tests and how
// each one landed, live (to-do progress → child).
function SoloPlan({
  taskId,
  agentId,
  issueId,
  showCaseChildren,
}: {
  taskId: string;
  agentId: string | null | undefined;
  issueId: string;
  showCaseChildren: boolean;
}) {
  const { todos } = useTaskLive(taskId);
  if (todos.length === 0) return null;
  const done = todos.filter((td) => td.status === "completed").length;
  // Nest the cases under the ACTIVE step; when nothing is in progress (e.g.
  // between steps), fall back to the last item so the children stay visible.
  const activeIdx = todos.findIndex((td) => td.status === "in_progress");
  const caseAnchorIdx = activeIdx >= 0 ? activeIdx : todos.length - 1;
  return (
    <PanelShell agentId={agentId} done={done} total={todos.length}>
      {todos.map((td, i) => {
        const state: PartState =
          td.status === "completed" ? "done" : td.status === "in_progress" ? "doing" : "pending";
        const label = state === "doing" ? (td.activeForm ?? td.content) : td.content;
        return (
          <li key={i} aria-current={state === "doing" || undefined}>
            <div className={rowClass(state)}>
              <StateIcon state={state} />
              <span className={labelClass(state)}>{label}</span>
            </div>
            {showCaseChildren && i === caseAnchorIdx && (
              <div className="pl-4">
                <QALiveCaseChildren issueId={issueId} />
              </div>
            )}
          </li>
        );
      })}
    </PanelShell>
  );
}

export function IssuePlanPanel({
  childIssues,
  currentTaskId,
  orchestratorAgentId,
  issueId,
  stage,
}: {
  /** The issue's sub-issues (the orchestrator's parts). Empty ⇒ solo issue. */
  childIssues: Issue[];
  /** The current stage's running task id — drives the solo-issue plan. */
  currentTaskId: string | null;
  /** The pipeline owner (squad lead / solo agent), shown next to the title. */
  orchestratorAgentId: string | null | undefined;
  /** The issue — needed to nest its live test-case runs under the QA step. */
  issueId: string;
  /** Current pipeline stage; "qa" nests the test-case children. */
  stage: string;
}) {
  // Orchestrated: real parts (sub-issues) as a live checklist.
  if (childIssues.length > 0) {
    const done = childIssues.filter((c) => issuePartState(c.status) === "done").length;
    return (
      <PanelShell agentId={orchestratorAgentId} done={done} total={childIssues.length}>
        {childIssues.map((c) => (
          <PartRow key={c.id} child={c} />
        ))}
      </PanelShell>
    );
  }

  // Solo: the single running agent's own deliverable plan (nothing when idle).
  if (!currentTaskId) return null;
  return (
    <SoloPlan
      taskId={currentTaskId}
      agentId={orchestratorAgentId}
      issueId={issueId}
      showCaseChildren={stage === "qa"}
    />
  );
}
