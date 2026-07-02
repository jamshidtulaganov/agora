"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Palette, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { agentListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { parseDesignManifest } from "@agora/core/design";
import { useT } from "../../i18n";

// Project design-manifest section. Surfaces the project's design system (kind,
// component count, revision, source), the designer-agent picker, the Figma
// library file key, a validated-JSON editor, and a "Generate with agent"
// button. ALL saves go through the KEY-SCOPED PUT /design-manifest endpoint
// (never the whole-blob updateProject merge), so a human save can't wipe a
// concurrent agent-written manifest or the project's qa_manifest.
export function ProjectDesignSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [syncing, setSyncing] = useState(false);

  const settings = project?.settings;
  const manifest = useMemo(() => parseDesignManifest(settings?.design_manifest), [settings?.design_manifest]);
  const savedAgent = (settings?.design_agent ?? "") as string;

  const [designAgent, setDesignAgent] = useState(savedAgent);
  const [jsonDraft, setJsonDraft] = useState("");
  const [jsonError, setJsonError] = useState<string | null>(null);

  useEffect(() => setDesignAgent(savedAgent), [savedAgent]);
  // Seed the editor from the current manifest (pretty-printed) on load/change.
  useEffect(() => {
    setJsonDraft(settings?.design_manifest ? JSON.stringify(settings.design_manifest, null, 2) : "");
  }, [settings?.design_manifest]);

  const refresh = () => qc.invalidateQueries({ queryKey: projectDetailOptions(wsId, projectId).queryKey });

  const saveAgent = async (value: string) => {
    if (value === savedAgent) return;
    try {
      await api.putDesignManifest(projectId, { design_agent: value });
      refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.design.saved_toast));
    }
  };

  const saveManifest = async () => {
    if (saving) return;
    let parsed: Record<string, unknown>;
    try {
      parsed = JSON.parse(jsonDraft || "{}");
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        throw new Error("not an object");
      }
    } catch {
      setJsonError(t(($) => $.design.invalid_json));
      return;
    }
    setJsonError(null);
    setSaving(true);
    try {
      await api.putDesignManifest(projectId, { manifest: parsed });
      refresh();
      toast.success(t(($) => $.design.saved_toast));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.design.invalid_json));
    } finally {
      setSaving(false);
    }
  };

  const generate = async () => {
    if (syncing) return;
    setSyncing(true);
    try {
      await api.syncDesignManifest(projectId);
      toast.success(t(($) => $.design.generate_fired_toast));
    } catch (e) {
      const msg = e instanceof Error ? e.message : "";
      toast.error(msg.includes("sync_already_running") ? t(($) => $.design.sync_running) : msg || t(($) => $.design.invalid_json));
    } finally {
      setSyncing(false);
    }
  };

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        <Palette className="!size-3 shrink-0 text-muted-foreground" />
        {t(($) => $.design.title)}
        <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
      </button>
      {open && (
        <div className="space-y-2 pl-2">
          <p className="text-[10px] text-muted-foreground">{t(($) => $.design.description)}</p>

          {manifest ? (
            <div className="flex flex-wrap items-center gap-1.5 text-[10px] text-muted-foreground">
              <span className="rounded bg-muted px-1.5 py-0.5 font-medium">
                {manifest.kind === "tokens" ? t(($) => $.design.kind_tokens) : t(($) => $.design.kind_inventory)}
              </span>
              <span>{t(($) => $.design.components_count, { count: manifest.components.length })}</span>
              <span>· {t(($) => $.design.revision)} {manifest.revision}</span>
              <span>
                ·{" "}
                {manifest.source === "manual"
                  ? t(($) => $.design.source_manual)
                  : manifest.source === "mixed"
                    ? t(($) => $.design.source_mixed)
                    : t(($) => $.design.source_agent)}
              </span>
            </div>
          ) : (
            <p className="text-[10px] text-muted-foreground">{t(($) => $.design.empty_state)}</p>
          )}

          <label className="block space-y-1">
            <span className="text-[10px] font-medium text-muted-foreground">{t(($) => $.design.agent_label)}</span>
            <select
              value={designAgent}
              onChange={(e) => {
                setDesignAgent(e.target.value);
                void saveAgent(e.target.value);
              }}
              className="h-7 w-full rounded-md border bg-transparent px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <option value="">{t(($) => $.design.agent_placeholder)}</option>
              {agents
                .filter((a) => !a.archived_at)
                .map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
            </select>
          </label>

          <label className="block space-y-1">
            <span className="text-[10px] font-medium text-muted-foreground">{t(($) => $.design.edit_json)}</span>
            <textarea
              value={jsonDraft}
              onChange={(e) => {
                setJsonDraft(e.target.value);
                if (jsonError) setJsonError(null);
              }}
              rows={8}
              spellCheck={false}
              placeholder='{ "kind": "inventory", "components": [] }'
              className="w-full rounded-md border bg-transparent px-2 py-1.5 font-mono text-[11px] outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </label>
          {jsonError && <p className="text-[10px] text-destructive">{jsonError}</p>}

          <div className="flex flex-wrap gap-1.5">
            <button
              type="button"
              onClick={() => void saveManifest()}
              disabled={saving}
              className="inline-flex h-7 items-center gap-1 rounded-md border px-2 text-xs hover:bg-accent/70 disabled:opacity-50"
            >
              {saving && <Loader2 className="size-3.5 animate-spin" />}
              {t(($) => $.design.save_json)}
            </button>
            <button
              type="button"
              onClick={() => void generate()}
              disabled={syncing}
              className="inline-flex h-7 items-center gap-1 rounded-md border px-2 text-xs hover:bg-accent/70 disabled:opacity-50"
            >
              {syncing ? <Loader2 className="size-3.5 animate-spin" /> : <Palette className="size-3.5" />}
              {t(($) => $.design.generate_button)}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
