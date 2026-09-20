"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  projectRiskMapOptions,
  pruneRiskMapEntries,
  riskMapDefaultTier,
  riskMapMaxEntries,
  riskMapTierOptions,
  useUpdateProjectRiskMap,
} from "@agora/core/projects/risk-map";
import type { RiskMapEntry } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@agora/ui/components/ui/native-select";
import { useT } from "../../i18n";

// The project risk map editor (docs/orchestration-upgrade-plan.md §A1.2).
//
// Risk is the pipeline's most load-bearing signal — it decides how deep QA
// goes, whether a human must sign off, and whether a change may merge on its
// own — and until this panel existed the map had no write endpoint at all.
//
// The map is a list of MODULE entries: a name a person would actually say in
// review ("billing"), its tier, the globs that make it up, and optionally an
// owner and a note. Deliberately quiet: add an entry, remove an entry, one
// Save. No wizard, no glob playground. Someone filling this in is writing
// down what they already know about their codebase.

function blankEntry(tier: string): RiskMapEntry {
  return { module: "", tier, paths: [], owner: "", notes: "" };
}

export function ProjectRiskMapSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const { data: saved } = useQuery(projectRiskMapOptions(wsId, projectId));
  const update = useUpdateProjectRiskMap(wsId, projectId);

  const [draft, setDraft] = useState<RiskMapEntry[]>([]);
  // Re-sync when the server value changes (another client, or the initial
  // load resolving after mount). The stored map is the floor; an unsaved
  // edit is lost on a remote change, which is the right trade for a safety
  // control that must not silently diverge from what the pipeline reads.
  useEffect(() => {
    setDraft(saved?.risk_map.map((e) => ({ ...e, paths: [...e.paths] })) ?? []);
  }, [saved]);

  const tiers = useMemo(() => riskMapTierOptions(saved), [saved]);
  const defaultTier = riskMapDefaultTier(saved);
  const maxEntries = riskMapMaxEntries(saved);
  const dirty = useMemo(
    () =>
      JSON.stringify(pruneRiskMapEntries(draft)) !==
      JSON.stringify(pruneRiskMapEntries(saved?.risk_map ?? [])),
    [draft, saved],
  );
  const atCap = draft.length >= maxEntries;

  const patch = (index: number, next: Partial<RiskMapEntry>) =>
    setDraft((prev) => prev.map((e, i) => (i === index ? { ...e, ...next } : e)));

  const save = () => {
    update.mutate(pruneRiskMapEntries(draft), {
      onSuccess: () => toast.success(t(($) => $.risk_map.saved)),
      onError: (e) =>
        toast.error(e instanceof Error && e.message ? e.message : t(($) => $.risk_map.save_failed)),
    });
  };

  return (
    <div className="mt-4 space-y-3">
      <div>
        <h4 className="text-xs font-medium">{t(($) => $.risk_map.title)}</h4>
        {/* One plain sentence, no jargon: what the tiers gate, and what
            happens to everything the map does not mention. */}
        <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.risk_map.explainer, { tier: defaultTier })}
        </p>
      </div>

      <div className="space-y-2">
        {draft.length === 0 ? (
          <p className="text-[11px] text-muted-foreground">{t(($) => $.risk_map.no_entries)}</p>
        ) : (
          draft.map((entry, index) => (
            <EntryCard
              key={index}
              entry={entry}
              tiers={tiers}
              onChange={(next) => patch(index, next)}
              onRemove={() => setDraft((prev) => prev.filter((_, i) => i !== index))}
            />
          ))
        )}
      </div>

      <div className="flex items-center gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-7 gap-1 text-[11px]"
          disabled={atCap}
          onClick={() => setDraft((prev) => [...prev, blankEntry(defaultTier)])}
        >
          <Plus className="size-3" />
          {t(($) => $.risk_map.add_entry)}
        </Button>
        <Button size="sm" className="h-7" disabled={!dirty || update.isPending} onClick={save}>
          {t(($) => $.risk_map.save)}
        </Button>
        {atCap && (
          <span className="text-[10px] text-muted-foreground">
            {t(($) => $.risk_map.at_cap, { max: maxEntries })}
          </span>
        )}
      </div>
    </div>
  );
}

function EntryCard({
  entry,
  tiers,
  onChange,
  onRemove,
}: {
  entry: RiskMapEntry;
  tiers: string[];
  onChange: (next: Partial<RiskMapEntry>) => void;
  onRemove: () => void;
}) {
  const { t } = useT("projects");
  const [path, setPath] = useState("");

  const addPath = () => {
    const glob = path.trim();
    if (!glob || entry.paths.includes(glob)) {
      setPath("");
      return;
    }
    onChange({ paths: [...entry.paths, glob] });
    setPath("");
  };

  const label = entry.module || t(($) => $.risk_map.untitled_module);

  return (
    <div className="space-y-2 rounded-md border px-3 py-2">
      <div className="flex items-center gap-1.5">
        <Input
          value={entry.module}
          onChange={(e) => onChange({ module: e.target.value })}
          placeholder={t(($) => $.risk_map.module_placeholder)}
          aria-label={t(($) => $.risk_map.module_label)}
          className="h-7 text-[11px]"
        />
        {/* Tier names are schema-level identifiers, so they stay lowercase
            English in every locale — same rule as issue status. */}
        <NativeSelect
          value={entry.tier}
          onChange={(e) => onChange({ tier: e.target.value })}
          aria-label={t(($) => $.risk_map.tier_label, { module: label })}
          className="h-7 w-32 font-mono text-[11px]"
        >
          {tiers.map((tier) => (
            <NativeSelectOption key={tier} value={tier}>
              {tier}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          className="shrink-0 text-muted-foreground"
          aria-label={t(($) => $.risk_map.remove_entry, { module: label })}
          onClick={onRemove}
        >
          <Trash2 className="size-3.5" />
        </Button>
      </div>

      <ul className="flex flex-wrap gap-1.5">
        {entry.paths.length === 0 ? (
          <li className="text-[11px] text-muted-foreground">{t(($) => $.risk_map.no_paths)}</li>
        ) : (
          entry.paths.map((glob) => (
            <li
              key={glob}
              className="flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]"
            >
              <span className="max-w-[22rem] truncate">{glob}</span>
              <button
                type="button"
                aria-label={t(($) => $.risk_map.remove_path, { glob })}
                className="text-muted-foreground hover:text-foreground"
                onClick={() => onChange({ paths: entry.paths.filter((g) => g !== glob) })}
              >
                <X className="size-3" />
              </button>
            </li>
          ))
        )}
      </ul>

      <div className="flex items-center gap-1.5">
        <Input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              addPath();
            }
          }}
          placeholder={t(($) => $.risk_map.path_placeholder)}
          aria-label={t(($) => $.risk_map.add_path_to, { module: label })}
          className="h-7 font-mono text-[11px]"
        />
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-7 shrink-0 gap-1 text-[11px]"
          disabled={!path.trim()}
          onClick={addPath}
        >
          <Plus className="size-3" />
          {t(($) => $.risk_map.add_path)}
        </Button>
      </div>

      <div className="flex items-center gap-1.5">
        <Input
          value={entry.owner}
          onChange={(e) => onChange({ owner: e.target.value })}
          placeholder={t(($) => $.risk_map.owner_placeholder)}
          aria-label={t(($) => $.risk_map.owner_label, { module: label })}
          className="h-7 text-[11px]"
        />
        <Input
          value={entry.notes}
          onChange={(e) => onChange({ notes: e.target.value })}
          placeholder={t(($) => $.risk_map.notes_placeholder)}
          aria-label={t(($) => $.risk_map.notes_label, { module: label })}
          className="h-7 text-[11px]"
        />
      </div>
    </div>
  );
}
