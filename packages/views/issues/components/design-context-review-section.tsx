"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Loader2, ShieldCheck, X } from "lucide-react";
import { toast } from "sonner";
import { useWorkspaceId } from "@agora/core";
import { api } from "@agora/core/api";
import { useCurrentMember } from "@agora/core/permissions";
import type { DesignContextDocument } from "@agora/core/design";
import { Button } from "@agora/ui/components/ui/button";
import { useT } from "../../i18n";

const designContextKey = (projectId: string) => ["design-context", projectId] as const;

export function DesignContextReviewSection({ projectId }: { projectId: string }) {
  const { t } = useT("issues");
  const workspaceId = useWorkspaceId();
  const { role } = useCurrentMember(workspaceId);
  const canReview = role === "owner" || role === "admin";
  const qc = useQueryClient();
  const [reviewing, setReviewing] = useState<"approve" | "reject" | null>(null);
  const { data } = useQuery({
    queryKey: designContextKey(projectId),
    queryFn: () => api.getProjectDesignContext(projectId),
  });
  const proposal = data?.proposal;
  if (!proposal) return null;

  const current = data?.active?.context;
  const next = proposal.context;
  const tokenChanges = listTokenChanges(current, next);
  const componentChanges = listComponentChanges(current, next);
  const conventionChanges = listRuleChanges(current?.conventions ?? [], next.conventions);

  const review = async (action: "approve" | "reject") => {
    if (reviewing) return;
    setReviewing(action);
    try {
      await api.reviewProjectDesignContext(projectId, action, proposal.base_revision);
      await qc.invalidateQueries({ queryKey: designContextKey(projectId) });
      toast.success(t(($) => action === "approve" ? $.design_context.approved : $.design_context.rejected));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t(($) => $.design_context.review_failed));
    } finally {
      setReviewing(null);
    }
  };

  return (
    <section>
      <div className="mb-2 flex items-center gap-1 px-2 py-1 text-xs font-medium">
        <ShieldCheck className="!size-3 shrink-0 text-muted-foreground" />
        {t(($) => $.design_context.title)}
        <span className="ml-auto rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-400">
          {t(($) => $.design_context.pending)}
        </span>
      </div>
      <div className="space-y-2 pl-2 text-xs">
        <p className="text-muted-foreground">{t(($) => $.design_context.description)}</p>
        <div className="rounded-md border bg-muted/20 px-2.5 py-2 text-muted-foreground">
          <p>{t(($) => $.design_context.revision, { revision: proposal.revision, base: proposal.base_revision })}</p>
          <p>{t(($) => $.design_context.diff, { tokens: tokenChanges.length, components: componentChanges.length })}</p>
          <p>{t(($) => $.design_context.sources, { count: next.sources.length })}</p>
          <p>{t(($) => $.design_context.freshness, { status: proposal.freshness.status })}</p>
        </div>
        <ChangeList title={t(($) => $.design_context.token_changes)} values={tokenChanges} />
        <ChangeList title={t(($) => $.design_context.component_changes)} values={componentChanges} />
        <ChangeList title={t(($) => $.design_context.convention_changes)} values={conventionChanges} />
        <ChangeList
          title={t(($) => $.design_context.source_list)}
          values={next.sources.map((source) => `${source.kind}: ${source.locator}${source.revision ? ` @ ${source.revision}` : ""} #${source.content_hash.slice(0, 12)}`)}
        />
        {canReview ? <div className="flex gap-1.5">
          <Button size="sm" onClick={() => void review("approve")} disabled={reviewing !== null}>
            {reviewing === "approve" ? <Loader2 className="size-3.5 animate-spin" /> : <Check className="size-3.5" />}
            {t(($) => $.design_context.approve)}
          </Button>
          <Button variant="outline" size="sm" onClick={() => void review("reject")} disabled={reviewing !== null}>
            {reviewing === "reject" ? <Loader2 className="size-3.5 animate-spin" /> : <X className="size-3.5" />}
            {t(($) => $.design_context.reject)}
          </Button>
        </div> : <p className="text-muted-foreground">{t(($) => $.design_context.admin_required)}</p>}
      </div>
    </section>
  );
}

function listTokenChanges(current: DesignContextDocument | undefined, next: DesignContextDocument): string[] {
  const changes: string[] = [];
  for (const category of ["colors", "typography", "spacing"] as const) {
    const before = current?.tokens[category] ?? {};
    const after = next.tokens[category];
    for (const key of new Set([...Object.keys(before), ...Object.keys(after)])) {
      if (before[key] !== after[key]) changes.push(`${category}.${key}: ${before[key] ?? "∅"} → ${after[key] ?? "∅"}`);
    }
  }
  return changes.sort();
}

function listComponentChanges(current: DesignContextDocument | undefined, next: DesignContextDocument): string[] {
  const before = new Map((current?.components ?? []).map((item) => [item.name.toLowerCase(), item]));
  const after = new Map(next.components.map((item) => [item.name.toLowerCase(), item]));
  const changes: string[] = [];
  for (const key of new Set([...before.keys(), ...after.keys()])) {
    const previous = before.get(key);
    const upcoming = after.get(key);
    if (JSON.stringify(previous) !== JSON.stringify(upcoming)) {
      changes.push(upcoming?.name ?? `${previous?.name ?? key} (removed)`);
    }
  }
  return changes.sort();
}

function listRuleChanges(current: string[], next: string[]): string[] {
  const before = new Set(current);
  const after = new Set(next);
  return [
    ...next.filter((value) => !before.has(value)).map((value) => `+ ${value}`),
    ...current.filter((value) => !after.has(value)).map((value) => `− ${value}`),
  ];
}

function ChangeList({ title, values }: { title: string; values: string[] }) {
  if (values.length === 0) return null;
  return (
    <details className="rounded-md border px-2.5 py-2">
      <summary className="cursor-pointer font-medium">{title} ({values.length})</summary>
      <ul className="mt-1.5 space-y-1 text-muted-foreground">
        {values.slice(0, 20).map((value) => <li key={value} className="break-all">{value}</li>)}
      </ul>
    </details>
  );
}
