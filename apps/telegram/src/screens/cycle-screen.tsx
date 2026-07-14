import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import { sprintDetailOptions, sprintIssuesOptions } from "@agora/core/sprints";
import { deriveStagePipeline, type SDLCStage } from "@agora/core/issues";
import type { Issue, WorkspaceSprint } from "@agora/core/types";
import { STAGE_DOT_BG, STAGE_TEXT } from "../components/stage-rail";
import { CenterMessage } from "../components/center-message";
import { TabSkeleton } from "../components/skeleton";
import { QueryError } from "../components/query-error";
import { useRouter } from "../platform/navigation";
import { haptic } from "../telegram/sdk";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

// Cycle tab (design 5a §2.3): active-sprint health at a glance — stage
// distribution bar, "needs a human" review queue, and per-stage counts.
// Stage is derived client-side per issue (no stage column exists); done /
// cancelled issues collapse into a synthetic "done" bucket.

type Bucket = SDLCStage | "done";

// Left-to-right pipeline order for the stacked bar + legend.
const SEGMENT_ORDER: Bucket[] = ["dev", "qa", "review", "done"];
// Attention-first order for the stages list (QA and Dev are where work piles up).
const LIST_ORDER: Bucket[] = ["qa", "dev", "review", "done"];

const BUCKET_BG: Record<Bucket, string> = { ...STAGE_DOT_BG, done: "bg-success" };
const BUCKET_TEXT: Record<Bucket, string> = { ...STAGE_TEXT, done: "text-success" };

const CARD =
  "rounded-xl border border-border bg-card shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none";

/** Whole days from today (local midnight) until the given ISO date; NaN if unparsable. */
function daysUntil(iso: string): number {
  const end = new Date(iso);
  if (Number.isNaN(end.getTime())) return NaN;
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const endDay = new Date(end.getFullYear(), end.getMonth(), end.getDate()).getTime();
  return Math.round((endDay - today) / 86_400_000);
}

export function CycleScreen() {
  const wsId = useWorkspaceId();
  const t = useT();
  const { navigate, openTab } = useRouter();

  // Workspace-wide sprint list; the "current sprint" is the first active one
  // (no dedicated endpoint — see coreApi map §3).
  const {
    data: sprints,
    isLoading: sprintsLoading,
    isError: sprintsError,
    refetch: refetchSprints,
  } = useQuery({
    queryKey: ["tg-ws-sprints", wsId],
    queryFn: async (): Promise<WorkspaceSprint[]> =>
      (await api.listWorkspaceSprints()) ?? [],
    refetchInterval: 30_000,
  });

  const activeSprints = useMemo(
    () => (sprints ?? []).filter((s) => s?.status === "active"),
    [sprints],
  );
  const sprint = activeSprints[0];

  // Detail carries the start/end dates the lightweight workspace list omits.
  const { data: detail } = useQuery({
    ...sprintDetailOptions(wsId, sprint?.id ?? ""),
    enabled: !!sprint,
    refetchInterval: 30_000,
  });

  const { data: issues, isLoading: issuesLoading } = useQuery({
    ...sprintIssuesOptions(wsId, sprint?.id ?? ""),
    enabled: !!sprint,
    refetchInterval: 20_000,
  });

  // Per-issue derived stage bucket. Closed issues collapse into "done".
  const derived = useMemo(() => {
    return (issues ?? []).map((issue: Issue) => {
      const pipeline = deriveStagePipeline({
        status: issue.status,
        labels: issue.labels ?? [],
      });
      const closed = issue.status === "done" || issue.status === "cancelled";
      const bucket: Bucket = closed ? "done" : pipeline.current;
      return { issue, bucket };
    });
  }, [issues]);

  const counts = useMemo(() => {
    const acc: Record<Bucket, number> = { dev: 0, qa: 0, review: 0, done: 0 };
    for (const d of derived) acc[d.bucket] += 1;
    return acc;
  }, [derived]);

  const total = derived.length;
  const segments = SEGMENT_ORDER.filter((b) => counts[b] > 0);
  const listBuckets = LIST_ORDER.filter((b) => counts[b] > 0);
  // Issues parked at the review gate — a human has to look at these.
  const needsHuman = derived.filter((d) => d.bucket === "review").slice(0, 3);

  if (sprintsLoading || (sprint && issuesLoading)) return <TabSkeleton />;
  if (sprintsError) return <QueryError onRetry={() => void refetchSprints()} />;

  if (!sprint) {
    return (
      <CenterMessage title={t("cycle.noSprint")} subtitle={t("cycle.noSprintSub")} />
    );
  }

  // Right-hand meta: sprint name, days-left (when an end date exists), and a
  // locale-neutral "+n" hint when several sprints are active at once.
  const metaParts: string[] = [sprint.name ?? ""];
  const endDate = detail?.end_date;
  if (endDate) {
    const n = daysUntil(endDate);
    if (!Number.isNaN(n)) {
      metaParts.push(n <= 0 ? t("cycle.endsToday") : t("cycle.daysLeft", { n }));
    }
  }
  if (activeSprints.length > 1) metaParts.push(`+${activeSprints.length - 1}`);

  const bucketLabel = (b: Bucket): string =>
    b === "done" ? t("status.done") : t(`stage.${b}`);

  const openIssue = (id: string) => {
    haptic("light");
    navigate({ name: "issue", id });
  };

  return (
    <div className="flex min-h-0 flex-1 animate-ag-fade-in flex-col gap-2.5 overflow-y-auto px-4 py-2.5">
      {/* Title row */}
      <div className="flex items-baseline gap-2.5 px-1 pb-1">
        <h1 className="flex-1 text-[26px] font-bold tracking-[-0.4px] text-foreground">
          {t("cycle.title")}
        </h1>
        <span className="max-w-[60%] truncate text-[13px] text-muted-foreground">
          {metaParts.filter(Boolean).join(" · ")}
        </span>
      </div>

      {/* Stage distribution card */}
      <section className={cn(CARD, "p-4")}>
        <div className="flex h-2.5 gap-[3px] overflow-hidden rounded-[5px]">
          {total === 0 ? (
            <span className="h-full flex-1 bg-border" />
          ) : (
            segments.map((b) => (
              <span
                key={b}
                className={cn("h-full min-w-[10px]", BUCKET_BG[b])}
                style={{ flex: counts[b] }}
              />
            ))
          )}
        </div>
        {segments.length > 0 && (
          <div className="mt-2.5 flex items-center justify-between gap-2">
            {segments.map((b) => (
              <span
                key={b}
                className={cn(
                  "whitespace-nowrap text-[11px] font-semibold",
                  BUCKET_TEXT[b],
                )}
              >
                {bucketLabel(b)} {counts[b]}
              </span>
            ))}
          </div>
        )}
      </section>

      {/* "Needs a human" review-gate cards (cap 3) */}
      {needsHuman.map(({ issue }) => (
        <button
          key={issue.id}
          type="button"
          onClick={() => openIssue(issue.id)}
          className={cn(
            CARD,
            "flex w-full items-center gap-3 px-4 py-[14px] text-left active:border-brand",
          )}
        >
          <span className="flex size-[34px] shrink-0 items-center justify-center rounded-[10px] bg-destructive/10">
            <TriangleAlert className="size-[17px] text-destructive" />
          </span>
          <span className="min-w-0 flex-1">
            <span className="line-clamp-1 text-sm font-semibold leading-[1.35] text-foreground">
              {issue.title}
            </span>
            <span className="mt-0.5 block truncate text-xs text-muted-foreground">
              <span className="font-mono">{issue.identifier}</span>
              {" · "}
              {t("cycle.needsHuman")}
            </span>
          </span>
          <span className="shrink-0 text-[13px] font-semibold text-brand">
            {t("cycle.review")}
          </span>
        </button>
      ))}

      {/* Per-stage counts — rows jump to the Tasks tab (full list lives there) */}
      {listBuckets.length > 0 && (
        <section className={cn(CARD, "overflow-hidden")}>
          <div className="px-4 pb-1 pt-3 text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
            {t("cycle.stages")}
          </div>
          {listBuckets.map((b, i) => (
            <button
              key={b}
              type="button"
              onClick={() => {
                haptic("light");
                openTab("tasks");
              }}
              className={cn(
                "flex min-h-[46px] w-full items-center gap-2.5 px-4 py-3 text-left active:bg-muted/50",
                i > 0 && "border-t border-border/60",
              )}
            >
              <span className={cn("size-2 shrink-0 rounded-full", BUCKET_BG[b])} />
              <span className="flex-1 text-sm font-medium text-foreground">
                {bucketLabel(b)}
              </span>
              <span className="text-sm text-muted-foreground">{counts[b]}</span>
            </button>
          ))}
        </section>
      )}
    </div>
  );
}
