"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Palette, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import { useCurrentWorkspace } from "@agora/core/paths";
import { parseDesignManifest } from "@agora/core/design";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Button } from "@agora/ui/components/ui/button";
import { useT } from "../../i18n";

// Workspace-level SHARED design manifest — the base every project in the
// workspace inherits (so e.g. sd-cs / sd-main / sd-billing converge on one
// SalesDoctor design system, each project overriding only its specifics).
// Admin-gated write via the key-scoped PUT endpoint. Read-only for non-admins.
export function WorkspaceDesignSection() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";

  const savedManifest = (workspace?.settings?.design_manifest ?? null) as Record<string, unknown> | null;
  const parsed = parseDesignManifest(savedManifest);

  const [json, setJson] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setJson(savedManifest ? JSON.stringify(savedManifest, null, 2) : "");
  }, [savedManifest]);

  const save = async () => {
    if (saving || !wsId) return;
    let obj: Record<string, unknown>;
    try {
      obj = JSON.parse(json || "{}");
      if (typeof obj !== "object" || obj === null || Array.isArray(obj)) throw new Error("not an object");
    } catch {
      setError(t(($) => $.workspace_design.invalid_json));
      return;
    }
    setError(null);
    setSaving(true);
    try {
      await api.putWorkspaceDesignManifest(wsId, obj);
      toast.success(t(($) => $.workspace_design.saved_toast));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.workspace_design.invalid_json));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="space-y-4">
      <h2 className="flex items-center gap-1.5 text-sm font-semibold">
        <Palette className="h-4 w-4" /> {t(($) => $.workspace_design.title)}
      </h2>
      <Card>
        <CardContent className="space-y-3 pt-5">
          <p className="text-xs text-muted-foreground">{t(($) => $.workspace_design.description)}</p>
          {parsed && (
            <div className="flex flex-wrap items-center gap-1.5 text-[10px] text-muted-foreground">
              <span className="rounded bg-muted px-1.5 py-0.5 font-medium">
                {parsed.kind === "tokens" ? t(($) => $.workspace_design.kind_tokens) : t(($) => $.workspace_design.kind_inventory)}
              </span>
              <span>{t(($) => $.workspace_design.components_count, { count: parsed.components.length })}</span>
              <span>· {t(($) => $.workspace_design.revision)} {parsed.revision}</span>
            </div>
          )}
          <textarea
            value={json}
            onChange={(e) => {
              setJson(e.target.value);
              if (error) setError(null);
            }}
            rows={10}
            spellCheck={false}
            placeholder='{ "kind": "tokens", "tokens": { "colors": { "primary": "#2563EB" } }, "components": [] }'
            className="w-full rounded-md border bg-transparent px-2 py-1.5 font-mono text-[11px] outline-none focus-visible:ring-1 focus-visible:ring-ring"
          />
          {error && <p className="text-[11px] text-destructive">{error}</p>}
          <Button onClick={() => void save()} disabled={saving} size="sm">
            {saving ? <Loader2 className="mr-1 size-3.5 animate-spin" /> : null}
            {t(($) => $.workspace_design.save)}
          </Button>
        </CardContent>
      </Card>
    </section>
  );
}
