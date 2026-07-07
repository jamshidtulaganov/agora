"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { KeyRound, Trash2 } from "lucide-react";
import { Input } from "@agora/ui/components/ui/input";
import { Button } from "@agora/ui/components/ui/button";
import { api } from "@agora/core/api";
import { useT } from "../../i18n";

const PROVIDERS = ["github", "gitlab"] as const;
type Provider = (typeof PROVIDERS)[number];

const PROVIDER_LABEL: Record<Provider, string> = { github: "GitHub", gitlab: "GitLab" };

// Editor account integration: the user pastes a PAT once; the daemon injects
// it into their co-code editor env (GH_TOKEN/GITHUB_TOKEN/GITLAB_TOKEN), so
// gh CLI + HTTPS git inside the editor terminal are authenticated without a
// per-worktree browser sign-in. Tokens are write-only — the API returns a
// masked tail only.
export function EditorAccountsSection() {
  const { t } = useT("settings");
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: ["editor-tokens"],
    queryFn: () => api.listEditorTokens(),
    staleTime: 30_000,
  });
  const tokens = data?.tokens ?? [];
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);

  const save = async (provider: Provider) => {
    const token = (drafts[provider] ?? "").trim();
    if (!token) return;
    setBusy(provider);
    try {
      await api.putEditorToken(provider, token);
      setDrafts((d) => ({ ...d, [provider]: "" }));
      toast.success(t(($) => $.editor_accounts.toast_saved));
      void qc.invalidateQueries({ queryKey: ["editor-tokens"] });
    } catch (e) {
      toast.error(e instanceof Error && e.message ? e.message : t(($) => $.editor_accounts.toast_save_failed));
    } finally {
      setBusy(null);
    }
  };

  const remove = async (provider: Provider) => {
    setBusy(provider);
    try {
      await api.deleteEditorToken(provider);
      toast.success(t(($) => $.editor_accounts.toast_removed));
      void qc.invalidateQueries({ queryKey: ["editor-tokens"] });
    } catch (e) {
      toast.error(e instanceof Error && e.message ? e.message : t(($) => $.editor_accounts.toast_remove_failed));
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="space-y-4">
      <div>
        <h2 className="text-sm font-semibold">{t(($) => $.editor_accounts.title)}</h2>
        <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.editor_accounts.description)}</p>
      </div>
      <div className="space-y-3">
        {PROVIDERS.map((provider) => {
          const existing = tokens.find((x) => x.provider === provider);
          return (
            <div key={provider} className="flex flex-wrap items-center gap-2">
              <span className="w-16 shrink-0 text-sm">{PROVIDER_LABEL[provider]}</span>
              {existing ? (
                <>
                  <span className="inline-flex items-center gap-1.5 rounded-md border bg-muted/40 px-2 py-1 font-mono text-xs text-muted-foreground">
                    <KeyRound className="h-3 w-3" aria-hidden />
                    {existing.masked}
                  </span>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-7 gap-1 text-xs text-muted-foreground hover:text-destructive"
                    disabled={busy === provider}
                    onClick={() => remove(provider)}
                  >
                    <Trash2 className="h-3.5 w-3.5" aria-hidden />
                    {t(($) => $.editor_accounts.remove)}
                  </Button>
                </>
              ) : (
                <>
                  <Input
                    type="password"
                    autoComplete="off"
                    className="h-8 w-72 text-xs"
                    placeholder={t(($) => $.editor_accounts.token_placeholder)}
                    value={drafts[provider] ?? ""}
                    onChange={(e) => setDrafts((d) => ({ ...d, [provider]: e.target.value }))}
                  />
                  <Button
                    size="sm"
                    className="h-8 text-xs"
                    disabled={busy === provider || !(drafts[provider] ?? "").trim()}
                    onClick={() => save(provider)}
                  >
                    {t(($) => $.editor_accounts.save)}
                  </Button>
                </>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
}
