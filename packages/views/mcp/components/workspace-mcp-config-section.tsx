/* eslint-disable i18next/no-literal-string -- internal MCP admin surface; follows the existing panel */
"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FileJson, KeyRound, Loader2, Save, ShieldCheck, Trash2, Upload } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { useCurrentMember } from "@agora/core/permissions";
import {
  parseMcpConfigText,
  mcpCredentialsOptions,
  useDeleteMcpCredential,
  usePutMcpCredential,
  useUpdateWorkspaceMcpConfig,
  workspaceMcpConfigOptions,
} from "@agora/core/mcp";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { Textarea } from "@agora/ui/components/ui/textarea";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@agora/ui/components/ui/alert-dialog";
import { toast } from "sonner";

const EMPTY_MCP_DOCUMENT = { mcpServers: {} };
const MAX_IMPORT_BYTES = 1024 * 1024;

function configToText(value: unknown): string {
  return JSON.stringify(value ?? EMPTY_MCP_DOCUMENT, null, 2);
}

function remoteAuthTargets(value: unknown): Array<{ name: string; headerName: string }> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return [];
  const servers = (value as { mcpServers?: unknown }).mcpServers;
  if (servers === null || typeof servers !== "object" || Array.isArray(servers)) return [];

  const targets: Array<{ name: string; headerName: string }> = [];
  for (const [name, rawEntry] of Object.entries(servers)) {
    if (rawEntry === null || typeof rawEntry !== "object" || Array.isArray(rawEntry)) continue;
    const entry = rawEntry as { url?: unknown; headers?: unknown };
    if (typeof entry.url !== "string" || !entry.url.trim()) continue;
    const headers = entry.headers;
    const headerName = headers !== null && typeof headers === "object" && !Array.isArray(headers)
      ? Object.keys(headers)[0] || "Authorization"
      : "Authorization";
    targets.push({ name, headerName });
  }
  return targets.sort((a, b) => a.name.localeCompare(b.name));
}

export function WorkspaceMcpConfigSection() {
  const workspaceId = useWorkspaceId();
  const { role, isLoading: memberLoading } = useCurrentMember(workspaceId);
  const canManage = role === "owner" || role === "admin";
  const query = useQuery(workspaceMcpConfigOptions(workspaceId, canManage));
  const credentialsQuery = useQuery(mcpCredentialsOptions(workspaceId));
  const update = useUpdateWorkspaceMcpConfig(workspaceId);
  const putCredential = usePutMcpCredential(workspaceId);
  const deleteCredential = useDeleteMcpCredential(workspaceId);
  const original = useMemo(
    () => configToText(query.data?.mcp_config ?? null),
    [query.data?.mcp_config],
  );
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(() => original);
  const [clearOpen, setClearOpen] = useState(false);
  const [authValues, setAuthValues] = useState<Record<string, string>>({});
  const [credentialToRemove, setCredentialToRemove] = useState<string | null>(null);

  const previousOriginal = useRef(original);
  useEffect(() => {
    setText((current) => current === previousOriginal.current ? original : current);
    previousOriginal.current = original;
  }, [original]);

  const validation = useMemo(() => parseMcpConfigText(text), [text]);
  const dirty = text !== original;
  const serverCount = validation.ok ? validation.serverCount : 0;
  const hasStoredConfig = query.data?.mcp_config != null;
  const remoteTargets = useMemo(
    () => remoteAuthTargets(query.data?.mcp_config),
    [query.data?.mcp_config],
  );
  const credentials = useMemo(
    () => new Map((credentialsQuery.data ?? []).map((item) => [item.server_name, item])),
    [credentialsQuery.data],
  );

  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  async function save() {
    if (!validation.ok) return;
    try {
      const response = await update.mutateAsync(validation.value);
      setText(configToText(response.mcp_config));
      toast.success(`Workspace MCP config saved · ${validation.serverCount} server${validation.serverCount === 1 ? "" : "s"}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not save the MCP config");
    }
  }

  async function clear() {
    try {
      await update.mutateAsync(null);
      setText(configToText(null));
      setClearOpen(false);
      toast.success("Workspace MCP config cleared");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not clear the MCP config");
    }
  }

  async function importFile(file: File | undefined) {
    if (!file) return;
    if (file.size > MAX_IMPORT_BYTES) {
      toast.error("mcp.json must be smaller than 1 MB");
      return;
    }
    try {
      const imported = await file.text();
      const result = parseMcpConfigText(imported);
      if (!result.ok) {
        toast.error(result.error);
        return;
      }
      setText(configToText(result.value));
      toast.success(`Imported ${result.serverCount} MCP server${result.serverCount === 1 ? "" : "s"} · review and save to apply`);
    } catch {
      toast.error("Could not read this JSON file");
    } finally {
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  }

  async function sealCredential(serverName: string, headerName: string) {
    const secret = authValues[serverName]?.trim();
    if (!secret) return;
    try {
      await putCredential.mutateAsync({
        serverName,
        data: { header_name: headerName, secret },
      });
      setAuthValues((current) => ({ ...current, [serverName]: "" }));
      toast.success(`Sealed auth saved for ${serverName}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not seal the MCP credential");
    }
  }

  async function removeCredential() {
    if (!credentialToRemove) return;
    try {
      await deleteCredential.mutateAsync(credentialToRemove);
      toast.success(`Sealed auth removed for ${credentialToRemove}`);
      setCredentialToRemove(null);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not remove the MCP credential");
    }
  }

  if (memberLoading) {
    return <Skeleton className="mb-6 h-48 w-full" />;
  }

  if (!canManage) {
    return (
      <section className="mb-6 rounded-lg border border-border bg-muted/20 p-4">
        <div className="flex items-center gap-2 text-sm font-medium">
          <FileJson className="size-4 text-muted-foreground" aria-hidden="true" />
          Workspace mcp.json
        </div>
        <p className="mt-2 text-sm text-muted-foreground">
          Workspace MCP defaults are managed by owners and admins. Agent-specific
          connections you can access remain visible below.
        </p>
      </section>
    );
  }

  if (query.isLoading) {
    return <Skeleton className="mb-6 h-72 w-full" />;
  }

  return (
    <section className="mb-6 overflow-hidden rounded-lg border border-border">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border bg-muted/25 px-4 py-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <FileJson className="size-4 text-muted-foreground" aria-hidden="true" />
            <h2 className="text-sm font-medium">Workspace mcp.json</h2>
            <span className="rounded bg-background px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground ring-1 ring-border">
              shared default
            </span>
            {validation.ok && (
              <span className="font-mono text-xs tabular-nums text-muted-foreground">
                {serverCount} server{serverCount === 1 ? "" : "s"}
              </span>
            )}
          </div>
          <p className="mt-1 max-w-3xl text-xs text-muted-foreground">
            Paste or import the same <code className="font-mono">mcpServers</code>{" "}
            JSON used by Claude Desktop and Cursor. Agora merges it into every
            agent&apos;s next task; an agent-specific server with the same name wins.
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <input
            ref={fileInputRef}
            type="file"
            accept=".json,application/json"
            className="sr-only"
            aria-label="Import mcp.json"
            onChange={(event) => void importFile(event.target.files?.[0])}
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => fileInputRef.current?.click()}
          >
            <Upload aria-hidden="true" />
            Import JSON
          </Button>
        </div>
      </div>

      <div className="space-y-3 p-4">
        {query.isError && (
          <div role="alert" className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
            <p>Could not load the workspace MCP config. Saving is paused to protect the current config.</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
            >
              {query.isFetching && <Loader2 className="animate-spin" aria-hidden="true" />}
              Retry
            </Button>
          </div>
        )}

        <Textarea
          value={text}
          onChange={(event) => setText(event.target.value)}
          aria-label="Workspace mcp.json editor"
          aria-invalid={!validation.ok || undefined}
          spellCheck={false}
          autoComplete="off"
          disabled={query.isError}
          className="min-h-64 resize-y font-mono text-xs leading-5"
        />

        {!validation.ok && (
          <p role="alert" className="text-xs text-destructive">
            {validation.error}
          </p>
        )}

        <div className="flex items-start gap-2 rounded-md border border-amber-500/25 bg-amber-500/5 px-3 py-2 text-xs text-muted-foreground">
          <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
          <p>
            Imported command, env, URL, and header values are admin-visible. For
            remote tokens, save the server with an empty header value and seal
            its token below instead of keeping a bearer token in JSON.
          </p>
        </div>

        {remoteTargets.length > 0 && (
          <div className="overflow-hidden rounded-md border border-border">
            <div className="flex items-center gap-2 border-b border-border bg-muted/25 px-3 py-2">
              <KeyRound className="size-3.5 text-muted-foreground" aria-hidden="true" />
              <h3 className="text-xs font-medium">Remote server auth</h3>
              <span className="text-xs text-muted-foreground">encrypted at rest</span>
            </div>
            <div className="divide-y divide-border">
              {remoteTargets.map(({ name, headerName }) => {
                const credential = credentials.get(name);
                const secret = authValues[name] ?? "";
                return (
                  <div key={name} className="grid gap-2 p-3 md:grid-cols-[minmax(9rem,0.7fr)_minmax(12rem,1.5fr)_auto] md:items-center">
                    <div className="min-w-0">
                      <p className="truncate font-mono text-xs font-medium">{name}</p>
                      <p className="truncate font-mono text-[10px] text-muted-foreground">{headerName}</p>
                    </div>
                    <Input
                      type="password"
                      value={secret}
                      onChange={(event) => setAuthValues((current) => ({ ...current, [name]: event.target.value }))}
                      aria-label={`Auth header value for ${name}`}
                      placeholder={credential?.has_secret ? `Sealed ••••${credential.last4}` : "Bearer <token>"}
                      autoComplete="new-password"
                      spellCheck={false}
                      className="font-mono text-xs"
                    />
                    <div className="flex items-center justify-end gap-2">
                      {credential?.has_secret && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => setCredentialToRemove(name)}
                          disabled={deleteCredential.isPending}
                        >
                          Remove auth
                        </Button>
                      )}
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => void sealCredential(name, headerName)}
                        disabled={!secret.trim() || putCredential.isPending}
                      >
                        {putCredential.isPending && <Loader2 className="animate-spin" aria-hidden="true" />}
                        {credential?.has_secret ? "Rotate" : "Seal token"}
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs text-muted-foreground" aria-live="polite">
            {dirty
              ? "Unsaved changes · applies to new tasks after saving"
              : hasStoredConfig
                ? "Saved · active on new tasks"
                : "No shared config · agents use their own runtime defaults"}
          </p>
          <div className="flex items-center gap-2">
            {hasStoredConfig && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setClearOpen(true)}
                disabled={update.isPending}
              >
                <Trash2 aria-hidden="true" />
                Clear shared config
              </Button>
            )}
            <Button
              type="button"
              size="sm"
              onClick={() => void save()}
              disabled={!dirty || !validation.ok || query.isError || update.isPending}
            >
              {update.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Save aria-hidden="true" />}
              Save shared config
            </Button>
          </div>
        </div>
      </div>

      <AlertDialog open={clearOpen} onOpenChange={setClearOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear workspace MCP config?</AlertDialogTitle>
            <AlertDialogDescription>
              Shared MCP servers will stop being added to new agent tasks.
              Existing per-agent MCP overrides are not changed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={update.isPending}>Keep config</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => void clear()} disabled={update.isPending}>
              {update.isPending && <Loader2 className="animate-spin" aria-hidden="true" />}
              Clear shared config
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={credentialToRemove !== null} onOpenChange={(open) => !open && setCredentialToRemove(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove sealed auth?</AlertDialogTitle>
            <AlertDialogDescription>
              New tasks using {credentialToRemove ?? "this server"} will no longer receive the stored auth header. The MCP server definition stays configured.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteCredential.isPending}>Keep auth</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => void removeCredential()} disabled={deleteCredential.isPending}>
              {deleteCredential.isPending && <Loader2 className="animate-spin" aria-hidden="true" />}
              Remove auth
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
