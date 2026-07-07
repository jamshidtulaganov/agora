"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Globe, KeyRound, Trash2, Users } from "lucide-react";
import { Input } from "@agora/ui/components/ui/input";
import { Button } from "@agora/ui/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agora/ui/components/ui/select";
import { api } from "@agora/core/api";
import { workspaceListOptions } from "@agora/core/workspace/queries";
import { useT } from "../../i18n";

const PROVIDERS = ["github", "gitlab"] as const;
type Provider = (typeof PROVIDERS)[number];

const PROVIDER_LABEL: Record<Provider, string> = { github: "GitHub", gitlab: "GitLab" };

// Editor account integration: the user pastes a PAT once; the daemon injects
// it into their co-code editor env (GH_TOKEN/GITHUB_TOKEN/GITLAB_TOKEN), so gh
// CLI + HTTPS git inside the editor terminal are authenticated without a
// per-worktree browser sign-in. A token is either the GLOBAL default or a
// per-WORKSPACE override (work vs personal identity per workspace) — an editor
// opened on a workspace's issue resolves workspace-specific first. Tokens are
// write-only — the API returns a masked tail only.
export function EditorAccountsSection() {
  const { t } = useT("settings");
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: ["editor-tokens"],
    queryFn: () => api.listEditorTokens(),
    staleTime: 30_000,
  });
  const { data: workspaces = [] } = useQuery(workspaceListOptions());
  const tokens = data?.tokens ?? [];

  const [provider, setProvider] = useState<Provider>("github");
  const [scope, setScope] = useState("global"); // "global" | workspace uuid
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);

  const wsName = (id: string) => workspaces.find((w) => w.id === id)?.name ?? t(($) => $.editor_accounts.scope_unknown_workspace);

  const save = async () => {
    const token = draft.trim();
    if (!token) return;
    setBusy(true);
    try {
      await api.putEditorToken(provider, token, scope === "global" ? undefined : scope);
      setDraft("");
      toast.success(t(($) => $.editor_accounts.toast_saved));
      void qc.invalidateQueries({ queryKey: ["editor-tokens"] });
    } catch (e) {
      toast.error(e instanceof Error && e.message ? e.message : t(($) => $.editor_accounts.toast_save_failed));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (p: string, workspaceId: string) => {
    setBusy(true);
    try {
      await api.deleteEditorToken(p as Provider, workspaceId || undefined);
      toast.success(t(($) => $.editor_accounts.toast_removed));
      void qc.invalidateQueries({ queryKey: ["editor-tokens"] });
    } catch (e) {
      toast.error(e instanceof Error && e.message ? e.message : t(($) => $.editor_accounts.toast_remove_failed));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="space-y-4">
      <div>
        <h2 className="text-sm font-semibold">{t(($) => $.editor_accounts.title)}</h2>
        <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.editor_accounts.description)}</p>
      </div>

      {tokens.length > 0 && (
        <div className="space-y-2">
          {tokens.map((row) => (
            <div key={`${row.provider}:${row.workspace_id}`} className="flex flex-wrap items-center gap-2">
              <span className="w-16 shrink-0 text-sm">{PROVIDER_LABEL[row.provider as Provider] ?? row.provider}</span>
              <span className="inline-flex items-center gap-1.5 rounded-md border bg-muted/40 px-2 py-1 text-xs text-muted-foreground">
                {row.workspace_id ? (
                  <>
                    <Users className="h-3 w-3" aria-hidden /> {wsName(row.workspace_id)}
                  </>
                ) : (
                  <>
                    <Globe className="h-3 w-3" aria-hidden /> {t(($) => $.editor_accounts.scope_global)}
                  </>
                )}
              </span>
              <span className="inline-flex items-center gap-1.5 rounded-md border bg-muted/40 px-2 py-1 font-mono text-xs text-muted-foreground">
                <KeyRound className="h-3 w-3" aria-hidden />
                {row.masked}
              </span>
              <Button
                size="sm"
                variant="ghost"
                className="h-7 gap-1 text-xs text-muted-foreground hover:text-destructive"
                disabled={busy}
                onClick={() => remove(row.provider, row.workspace_id)}
              >
                <Trash2 className="h-3.5 w-3.5" aria-hidden />
                {t(($) => $.editor_accounts.remove)}
              </Button>
            </div>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Select value={provider} onValueChange={(v) => setProvider((v as Provider) ?? "github")}>
          <SelectTrigger className="h-8 w-28 text-xs">
            <SelectValue>{() => PROVIDER_LABEL[provider]}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            {PROVIDERS.map((p) => (
              <SelectItem key={p} value={p}>
                {PROVIDER_LABEL[p]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={scope} onValueChange={(v) => setScope(v ?? "global")}>
          <SelectTrigger className="h-8 w-48 text-xs">
            <SelectValue>
              {() => (scope === "global" ? t(($) => $.editor_accounts.scope_global) : wsName(scope))}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="global">{t(($) => $.editor_accounts.scope_global)}</SelectItem>
            {workspaces.map((w) => (
              <SelectItem key={w.id} value={w.id}>
                {w.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          type="password"
          autoComplete="off"
          className="h-8 w-64 text-xs"
          placeholder={t(($) => $.editor_accounts.token_placeholder)}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <Button size="sm" className="h-8 text-xs" disabled={busy || !draft.trim()} onClick={save}>
          {t(($) => $.editor_accounts.save)}
        </Button>
      </div>
    </section>
  );
}
