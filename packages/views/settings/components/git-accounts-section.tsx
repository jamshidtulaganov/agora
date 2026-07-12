/* eslint-disable i18next/no-literal-string -- git accounts admin panel; i18n follow-up */
"use client";

import { useState } from "react";
import { Plus, Trash2, KeyRound } from "lucide-react";
import { Input } from "@agora/ui/components/ui/input";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import type { GitCredential } from "@agora/core/types";

// Per-workspace git accounts (PATs). The daemon matches a repo's host+owner to
// one of these to clone private repos across several accounts (e.g. two GitHub
// accounts for two companies). Tokens are write-only — never returned by the API.
export function GitAccountsSection({ workspaceId }: { workspaceId: string }) {
  const qc = useQueryClient();
  const { data: creds = [] } = useQuery({
    queryKey: ["git-credentials", workspaceId],
    queryFn: () => api.listGitCredentials(workspaceId),
    enabled: !!workspaceId,
  });

  const [owner, setOwner] = useState("");
  const [host, setHost] = useState("github.com");
  const [username, setUsername] = useState("");
  const [label, setLabel] = useState("");
  const [secret, setSecret] = useState("");
  const [saving, setSaving] = useState(false);

  const refresh = () => qc.invalidateQueries({ queryKey: ["git-credentials", workspaceId] });

  const add = async () => {
    if (!owner.trim() || !secret.trim()) {
      toast.error("Owner and token are required");
      return;
    }
    setSaving(true);
    try {
      await api.addGitCredential(workspaceId, {
        owner: owner.trim(),
        host: host.trim() || "github.com",
        username: username.trim(),
        label: label.trim(),
        secret: secret.trim(),
      });
      setOwner("");
      setUsername("");
      setLabel("");
      setSecret("");
      setHost("github.com");
      refresh();
      toast.success("Git account added");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to add account");
    } finally {
      setSaving(false);
    }
  };

  const remove = async (c: GitCredential) => {
    try {
      await api.deleteGitCredential(workspaceId, c.id);
      refresh();
      toast.success("Removed");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to remove");
    }
  };

  return (
    <Card>
      <CardContent className="space-y-4 pt-5">
        <div>
          <h3 className="flex items-center gap-1.5 text-sm font-semibold">
            <KeyRound className="h-4 w-4" /> Git accounts
          </h3>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Personal access tokens so agents can clone private repos across several
            accounts. A repo is matched to an account by host + owner. Tokens are
            encrypted and never shown again.
          </p>
        </div>

        {creds.length > 0 && (
          <ul className="divide-y divide-border rounded-md border border-border">
            {creds.map((c) => (
              <li key={c.id} className="flex items-center gap-2 px-3 py-2 text-sm">
                <span className="font-mono text-xs text-muted-foreground">{c.host}/</span>
                <span className="truncate font-medium">{c.owner}</span>
                {c.label && c.label !== c.owner && (
                  <span className="truncate text-xs text-muted-foreground">· {c.label}</span>
                )}
                <span className="ml-auto shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                  {c.auth_kind}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7 shrink-0"
                  aria-label={`Remove ${c.owner}`}
                  onClick={() => remove(c)}
                >
                  <Trash2 className="h-3.5 w-3.5 text-destructive" />
                </Button>
              </li>
            ))}
          </ul>
        )}

        <div className="grid grid-cols-2 gap-2">
          <Input
            placeholder="owner (e.g. jamshid-tulaganov)"
            value={owner}
            onChange={(e) => setOwner(e.target.value)}
          />
          <Input
            placeholder="host (github.com)"
            value={host}
            onChange={(e) => setHost(e.target.value)}
          />
          <Input
            placeholder="username (optional)"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
          <Input
            placeholder="label (optional)"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
          />
          <Input
            className="col-span-2"
            type="password"
            autoComplete="off"
            placeholder="personal access token (Contents: Read)"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
          />
        </div>
        <Button onClick={add} disabled={saving} size="sm">
          <Plus className="mr-1 h-3.5 w-3.5" /> {saving ? "Adding…" : "Add account"}
        </Button>
      </CardContent>
    </Card>
  );
}
