/* eslint-disable i18next/no-literal-string -- project admin panel; i18n follow-up */
"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, RotateCcw, Sliders } from "lucide-react";
import { toast } from "sonner";
import { projectConfigOptions } from "@agora/core/projects/queries";
import { useSetProjectConfig, useResetProjectConfig } from "@agora/core/projects/mutations";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ProjectConfigEntry } from "@agora/core/api/schemas";
import { Switch } from "@agora/ui/components/ui/switch";

// Project pipeline config — the per-project overrides for the ProjectScoped QA /
// review / automation behavior flags (auto-QA, fail-autoroute, auto-review,
// etc.). Each row shows the EFFECTIVE value for this project and where it came
// from: a project override ("Custom"), or the workspace default. Toggling sets a
// project override; Reset clears it back to the default. Owner/admin only
// server-side; the section still renders read-only for others (the toggle just
// 403s, surfaced as a toast). Mirrors ProjectQASection's collapsible shell.
//
// PLAIN_COPY reframes each flag as a plain PROJECT setting a PM reads — no
// run_qa / qa:pass / in_review→done / slice-action jargon. The backend registry
// keeps its engineer-facing wording for the instance-level Settings → Configs;
// this dictionary is the project-surface voice. Falls back to the API label /
// description for any key without an entry here.
const PLAIN_COPY: Record<string, { label: string; description: string }> = {
  AGORA_AUTO_QA_ENABLED: {
    label: "Test work automatically",
    description: "When an issue is ready for review, run its QA checks without anyone asking.",
  },
  AGORA_QA_GATE_ENFORCED: {
    label: "QA must pass before Done",
    description: "An issue can't be closed until its QA checks pass.",
  },
  AGORA_QA_FAIL_AUTOROUTE_ENABLED: {
    label: "Send failed work back to the team",
    description: "If QA fails, hand the issue straight back to the developers to fix.",
  },
  AGORA_QA_FAIL_AUTO_FILE_BUG_ENABLED: {
    label: "Open a bug when QA fails",
    description: "File a tracked bug automatically whenever a QA check fails.",
  },
  AGORA_QA_DISCRIMINATION_ENFORCED: {
    label: "Require a test that proves the fix",
    description: "A pass only counts when a test actually catches the change — failing before the fix, passing after.",
  },
  AGORA_RISK_TIER_GATE_ENFORCED: {
    label: "Extra care for risky areas",
    description: "Require deeper testing and a human sign-off on high-risk parts of the code.",
  },
  AGORA_AUTO_REVIEW_ENABLED: {
    label: "Code-review after testing",
    description: "Once QA passes, have an agent review the pull request before it merges.",
  },
  AGORA_AUTO_DOCS_ENABLED: {
    label: "Keep the docs up to date",
    description: "Update the project's docs automatically as work ships.",
  },
};

// Group headings in the same plain voice.
const CATEGORY_LABEL: Record<string, string> = {
  QA: "Testing",
  Review: "Code review",
  Automation: "Automation",
};
export function ProjectPipelineSection({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const [open, setOpen] = useState(false);
  const { data: entries = [] } = useQuery({
    ...projectConfigOptions(wsId, projectId),
    enabled: open, // only fetch once the section is expanded
  });
  const setConfig = useSetProjectConfig(projectId);
  const resetConfig = useResetProjectConfig(projectId);

  const onError = (e: unknown) =>
    toast.error(e instanceof Error && e.message ? e.message : "Couldn't update the setting");

  const setBool = (entry: ProjectConfigEntry, next: boolean) => {
    setConfig.mutate({ key: entry.key, value: next ? "true" : "false" }, { onError });
  };
  const setInt = (entry: ProjectConfigEntry, raw: string) => {
    const v = raw.trim();
    if (v === "" || !/^\d+$/.test(v)) return; // ignore empties / non-numbers
    if (v === entry.value) return;
    setConfig.mutate({ key: entry.key, value: v }, { onError });
  };
  const reset = (entry: ProjectConfigEntry) => {
    resetConfig.mutate(entry.key, { onError });
  };

  // Group by category (QA / Review / Automation) in first-seen order.
  const groups: { category: string; rows: ProjectConfigEntry[] }[] = [];
  for (const e of entries) {
    let g = groups.find((x) => x.category === e.category);
    if (!g) {
      g = { category: e.category || "General", rows: [] };
      groups.push(g);
    }
    g.rows.push(e);
  }

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        <Sliders className="!size-3 shrink-0 text-muted-foreground" />
        Pipeline
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
        />
      </button>
      {open && (
        <div className="space-y-3 pl-2">
          <p className="text-[10px] text-muted-foreground">
            How this project handles testing, review, and automation. Each setting
            uses the workspace default until you customize it for this project.
          </p>
          {groups.map((g) => (
            <div key={g.category} className="space-y-1">
              <div className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground/70">
                {CATEGORY_LABEL[g.category] ?? g.category}
              </div>
              <div className="divide-y rounded-lg border">
                {g.rows.map((entry) => (
                  <ConfigRow
                    key={entry.key}
                    entry={entry}
                    onToggle={(next) => setBool(entry, next)}
                    onSetInt={(v) => setInt(entry, v)}
                    onReset={() => reset(entry)}
                    busy={setConfig.isPending || resetConfig.isPending}
                  />
                ))}
              </div>
            </div>
          ))}
          {open && entries.length === 0 && (
            <p className="text-[11px] text-muted-foreground">No pipeline settings.</p>
          )}
        </div>
      )}
    </div>
  );
}

function ConfigRow({
  entry,
  onToggle,
  onSetInt,
  onReset,
  busy,
}: {
  entry: ProjectConfigEntry;
  onToggle: (next: boolean) => void;
  onSetInt: (value: string) => void;
  onReset: () => void;
  busy: boolean;
}) {
  const overridden = entry.overridden_by_project === true;
  const isBool = entry.kind === "bool";
  const truthy = entry.value === "true" || entry.value === "1";
  // Plain project-facing copy; fall back to the API wording for any key without
  // a plain entry (a new backend flag renders, just less polished).
  const copy = PLAIN_COPY[entry.key];
  const label = copy?.label ?? entry.label ?? entry.key;
  const description = copy?.description ?? entry.description;

  return (
    <div className="flex items-start gap-3 px-3 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="text-xs font-medium">{label}</span>
          {overridden ? (
            <span className="rounded bg-brand/15 px-1 py-0.5 text-[9px] font-medium text-brand">
              Custom
            </span>
          ) : (
            <span className="rounded bg-muted px-1 py-0.5 text-[9px] font-medium text-muted-foreground">
              Default
            </span>
          )}
        </div>
        {description && (
          <p className="mt-0.5 text-[11px] leading-snug text-muted-foreground">
            {description}
          </p>
        )}
        {overridden && (
          <button
            type="button"
            onClick={onReset}
            disabled={busy}
            className="mt-1 inline-flex items-center gap-1 text-[10px] text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
          >
            <RotateCcw className="size-2.5" />
            Use the workspace default
          </button>
        )}
      </div>
      <div className="shrink-0 pt-0.5">
        {isBool ? (
          <Switch checked={truthy} disabled={busy} onCheckedChange={onToggle} />
        ) : (
          <input
            type="number"
            defaultValue={entry.value}
            disabled={busy}
            onBlur={(e) => onSetInt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") (e.target as HTMLInputElement).blur();
            }}
            className="h-7 w-16 rounded-md border bg-transparent px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
          />
        )}
      </div>
    </div>
  );
}
