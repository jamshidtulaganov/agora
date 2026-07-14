/* eslint-disable i18next/no-literal-string -- internal MCP-servers admin panel; i18n is a follow-up */
"use client";

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Globe, Loader2, Lock, Plug, Plus, ShieldCheck, Terminal, Trash2, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  agentListOptions,
  mcpCredentialsOptions,
  useUpdateAgentMcpConfig,
  usePutMcpCredential,
  buildServerEntry,
  isRemoteMcpServer,
  listMcpServers,
  removeMcpServer,
  upsertMcpServer,
  MCP_SERVER_TEMPLATES,
  type McpServerTemplate,
  type McpTransport,
} from "@agora/core/mcp";
import type { Agent } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { Label } from "@agora/ui/components/ui/label";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { toast } from "sonner";
import { PageHeader } from "../../layout/page-header";

interface EnvRow {
  key: string;
  value: string;
}

/** Whether the given transport is a remote (URL-based) MCP server. */
function isRemoteTransport(t: McpTransport): t is "http" | "sse" {
  return t === "http" || t === "sse";
}

/**
 * Workspace-level MCP servers admin. Lists every agent and the MCP servers
 * each has configured (parsed from `agent.mcp_config`), and offers an
 * "Add MCP server" form that merges a new server into the selected agents'
 * configs. Mirrors the Bitrix import panel's structure (PageHeader + flat
 * scrolling body, hardcoded English copy). Drives `PUT /api/agents/{id}` via
 * useUpdateAgentMcpConfig.
 */
export function McpServersPanel() {
  const wsId = useWorkspaceId();
  const agentsQuery = useQuery(agentListOptions(wsId));
  const credsQuery = useQuery(mcpCredentialsOptions(wsId));
  const updateMut = useUpdateAgentMcpConfig(wsId);
  const putCredMut = usePutMcpCredential(wsId);

  const agents = useMemo(
    () => (agentsQuery.data ?? []).filter((a) => !a.archived_at),
    [agentsQuery.data],
  );

  // server name -> last4 hint for every remote server that has sealed auth
  // configured at the workspace level. Drives the "sealed auth" badge.
  const sealedAuth = useMemo(() => {
    const m = new Map<string, string>();
    for (const c of credsQuery.data ?? []) {
      if (c.has_secret) m.set(c.server_name, c.last4);
    }
    return m;
  }, [credsQuery.data]);

  // --- Add-server form state ---
  const [transport, setTransport] = useState<McpTransport>("stdio");
  const [name, setName] = useState("");
  // stdio fields
  const [command, setCommand] = useState("");
  const [argsText, setArgsText] = useState("");
  const [env, setEnv] = useState<EnvRow[]>([{ key: "", value: "" }]);
  // remote (http/sse) fields
  const [url, setUrl] = useState("");
  const [authHeaderName, setAuthHeaderName] = useState("Authorization");
  const [authHeaderValue, setAuthHeaderValue] = useState("");
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  // Required-scope hint for source-control templates (github/gitlab) + the
  // remote sealed-auth note, shown once a template that declares one is applied.
  const [scopeHint, setScopeHint] = useState<string | null>(null);
  // The add-server form is collapsed by default; the header row is a real
  // toggle button now (it read as clickable — a "+ Add MCP server" pill — but
  // was an inert div that did nothing when clicked).
  const [showAddForm, setShowAddForm] = useState(false);

  const remote = isRemoteTransport(transport);
  const selectedIds = Object.keys(selected).filter((id) => selected[id]);
  const allSelected =
    agents.length > 0 && selectedIds.length === agents.length;

  function toggleAgent(id: string) {
    setSelected((s) => ({ ...s, [id]: !s[id] }));
  }
  function toggleAll() {
    setSelected(
      allSelected ? {} : Object.fromEntries(agents.map((a) => [a.id, true])),
    );
  }

  function setEnvRow(i: number, patch: Partial<EnvRow>) {
    setEnv((rows) => rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  }
  function addEnvRow() {
    setEnv((rows) => [...rows, { key: "", value: "" }]);
  }
  function removeEnvRow(i: number) {
    setEnv((rows) =>
      rows.length === 1 ? [{ key: "", value: "" }] : rows.filter((_, idx) => idx !== i),
    );
  }

  function applyTemplate(tpl: McpServerTemplate) {
    setName(tpl.name);
    const t: McpTransport = tpl.transport ?? "stdio";
    setTransport(t);
    if (isRemoteTransport(t)) {
      setUrl(tpl.url ?? "");
      setAuthHeaderName(tpl.headerKeys?.[0] ?? "Authorization");
      setAuthHeaderValue("");
    } else {
      setCommand(tpl.command ?? "");
      setArgsText(tpl.argsText ?? "");
      setEnv(
        tpl.envKeys?.length
          ? tpl.envKeys.map((key) => ({ key, value: "" }))
          : [{ key: "", value: "" }],
      );
    }
    setScopeHint(tpl.scopeHint ?? null);
  }

  function resetForm() {
    setTransport("stdio");
    setName("");
    setCommand("");
    setArgsText("");
    setEnv([{ key: "", value: "" }]);
    setUrl("");
    setAuthHeaderName("Authorization");
    setAuthHeaderValue("");
    setSelected({});
    setScopeHint(null);
  }

  const busy = updateMut.isPending || putCredMut.isPending;
  const canSubmit =
    name.trim().length > 0 &&
    selectedIds.length > 0 &&
    !busy &&
    (remote ? url.trim().length > 0 : command.trim().length > 0);

  async function addServer() {
    const trimmedName = name.trim();
    if (!trimmedName || !selectedIds.length) return;
    if (remote ? !url.trim() : !command.trim()) return;

    // A remote entry stores { type, url } plus the auth header KEY with a blank
    // value (a documented placeholder); the header VALUE is sealed separately
    // in the workspace mcp_credential store and merged in server-side at
    // dispatch. A stdio entry stores command/args/env as before.
    const entry = remote
      ? buildServerEntry({
          transport,
          url,
          headers: authHeaderName.trim() ? [{ key: authHeaderName.trim(), value: "" }] : [],
        })
      : buildServerEntry({ command, argsText, env });

    // Apply to each selected agent in sequence: re-read its current config,
    // merge the new server under mcpServers[name], persist the whole object.
    const targets = agents.filter((a) => selected[a.id]);
    let ok = 0;
    for (const agent of targets) {
      if (agent.mcp_config_redacted === true) continue; // can't safely merge a hidden config
      const next = upsertMcpServer(agent.mcp_config, trimmedName, entry);
      try {
        await updateMut.mutateAsync({ agentId: agent.id, mcp_config: next });
        ok += 1;
      } catch (err) {
        toast.error(
          `Failed to update ${agent.name}: ${err instanceof Error ? err.message : "unknown error"}`,
        );
      }
    }

    // Seal the auth token once at the workspace level (keyed by server name), so
    // it covers every agent that has this remote server. Only when a value was
    // entered — leaving it blank keeps any existing sealed credential untouched.
    let sealFailed = false;
    if (ok > 0 && remote && authHeaderValue.trim()) {
      try {
        await putCredMut.mutateAsync({
          serverName: trimmedName,
          data: {
            header_name: authHeaderName.trim() || "Authorization",
            secret: authHeaderValue.trim(),
          },
        });
      } catch (err) {
        sealFailed = true;
        toast.error(
          `Server added, but sealing the auth token failed: ${err instanceof Error ? err.message : "unknown error"}. Re-enter the token to retry.`,
        );
      }
    }

    if (ok > 0 && !sealFailed) {
      toast.success(
        `Added "${trimmedName}" to ${ok} agent${ok === 1 ? "" : "s"}.`,
      );
      resetForm();
    }
  }

  async function deleteServer(agent: Agent, serverName: string) {
    const next = removeMcpServer(agent.mcp_config, serverName);
    try {
      await updateMut.mutateAsync({ agentId: agent.id, mcp_config: next });
      toast.success(`Removed "${serverName}" from ${agent.name}.`);
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to remove server",
      );
    }
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <Plug className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">MCP servers</h1>
          {agents.length > 0 && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
              {agents.length}
            </span>
          )}
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto p-5">
        <p className="mb-4 text-sm text-muted-foreground">
          Configure Model Context Protocol servers for the agents in this
          workspace. Each server is stored in the agent&apos;s{" "}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
            mcp_config
          </code>{" "}
          and forwarded to its runtime at launch.
        </p>

        {agentsQuery.isError && (
          <div className="mb-4 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            Failed to load agents — {(agentsQuery.error as Error)?.message}
          </div>
        )}

        {/* ---------------- Add MCP server form ---------------- */}
        <section className="mb-8 rounded-md border border-border">
          <button
            type="button"
            onClick={() => setShowAddForm((v) => !v)}
            aria-expanded={showAddForm}
            className="flex w-full items-center gap-2 border-b border-border bg-muted/30 px-3 py-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground"
          >
            <Plus className="h-3.5 w-3.5" />
            Add MCP server
            {showAddForm ? (
              <ChevronDown className="ml-auto h-3.5 w-3.5" />
            ) : (
              <ChevronRight className="ml-auto h-3.5 w-3.5" />
            )}
          </button>
          {showAddForm && (
          <div className="space-y-4 p-4">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs text-muted-foreground">
                Quick templates:
              </span>
              {MCP_SERVER_TEMPLATES.map((tpl) => (
                <Button
                  key={tpl.id}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => applyTemplate(tpl)}
                >
                  {tpl.label}
                </Button>
              ))}
            </div>

            {scopeHint && (
              <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-muted-foreground">
                {scopeHint}
              </div>
            )}

            {/* Transport toggle: local stdio command vs remote URL. */}
            <div className="space-y-1.5">
              <Label>Transport</Label>
              <div className="inline-flex rounded-md border border-border p-0.5">
                <button
                  type="button"
                  onClick={() => setTransport("stdio")}
                  className={`flex items-center gap-1.5 rounded px-3 py-1 text-xs font-medium transition-colors ${
                    !remote
                      ? "bg-background text-foreground shadow-sm"
                      : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  <Terminal className="h-3.5 w-3.5" />
                  Local command
                </button>
                <button
                  type="button"
                  onClick={() => setTransport((t) => (isRemoteTransport(t) ? t : "http"))}
                  className={`flex items-center gap-1.5 rounded px-3 py-1 text-xs font-medium transition-colors ${
                    remote
                      ? "bg-background text-foreground shadow-sm"
                      : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  <Globe className="h-3.5 w-3.5" />
                  Remote URL
                </button>
              </div>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="mcp-name">Server name</Label>
              <Input
                id="mcp-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={remote ? "linear" : "figma"}
                autoComplete="off"
                spellCheck={false}
              />
            </div>

            {remote ? (
              <>
                <div className="grid gap-4 sm:grid-cols-[9rem_1fr]">
                  <div className="space-y-1.5">
                    <Label>Type</Label>
                    <div className="inline-flex rounded-md border border-border p-0.5">
                      {(["http", "sse"] as const).map((t) => (
                        <button
                          key={t}
                          type="button"
                          onClick={() => setTransport(t)}
                          className={`rounded px-3 py-1 text-xs font-medium uppercase transition-colors ${
                            transport === t
                              ? "bg-background text-foreground shadow-sm"
                              : "text-muted-foreground hover:text-foreground"
                          }`}
                        >
                          {t}
                        </button>
                      ))}
                    </div>
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="mcp-url">URL</Label>
                    <Input
                      id="mcp-url"
                      value={url}
                      onChange={(e) => setUrl(e.target.value)}
                      placeholder="https://mcp.example.com/mcp"
                      autoComplete="off"
                      spellCheck={false}
                      className="font-mono text-xs"
                    />
                  </div>
                </div>

                <div className="space-y-2">
                  <Label>Auth header (sealed)</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      value={authHeaderName}
                      onChange={(e) => setAuthHeaderName(e.target.value)}
                      placeholder="Authorization"
                      autoComplete="off"
                      spellCheck={false}
                      className="w-48 font-mono text-xs"
                      aria-label="Auth header name"
                    />
                    <Input
                      value={authHeaderValue}
                      onChange={(e) => setAuthHeaderValue(e.target.value)}
                      type="password"
                      placeholder="Bearer <token>"
                      autoComplete="off"
                      spellCheck={false}
                      className="flex-1 font-mono text-xs"
                      aria-label="Auth header value"
                    />
                  </div>
                  <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
                    <ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    The token is stored sealed (secretbox) and injected
                    server-side at task dispatch. It never lands in the
                    agent&apos;s mcp_config. Leave blank to keep an existing
                    sealed token unchanged.
                  </p>
                </div>
              </>
            ) : (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor="mcp-command">Command</Label>
                  <Input
                    id="mcp-command"
                    value={command}
                    onChange={(e) => setCommand(e.target.value)}
                    placeholder="npx"
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono text-xs"
                  />
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor="mcp-args">Args</Label>
                  <Input
                    id="mcp-args"
                    value={argsText}
                    onChange={(e) => setArgsText(e.target.value)}
                    placeholder="-y figma-developer-mcp --stdio"
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono text-xs"
                  />
                  <p className="text-xs text-muted-foreground">
                    Space-separated. Stored as an array of strings.
                  </p>
                </div>

                <div className="space-y-2">
                  <Label>Environment variables</Label>
                  {env.map((row, i) => (
                    <div key={i} className="flex items-center gap-2">
                      <Input
                        value={row.key}
                        onChange={(e) => setEnvRow(i, { key: e.target.value })}
                        placeholder="FIGMA_API_KEY"
                        autoComplete="off"
                        spellCheck={false}
                        className="flex-1 font-mono text-xs"
                        aria-label="Env key"
                      />
                      <Input
                        value={row.value}
                        onChange={(e) => setEnvRow(i, { value: e.target.value })}
                        placeholder="value"
                        autoComplete="off"
                        spellCheck={false}
                        className="flex-1 font-mono text-xs"
                        aria-label="Env value"
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => removeEnvRow(i)}
                        aria-label="Remove env row"
                      >
                        <X className="h-4 w-4" />
                      </Button>
                    </div>
                  ))}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={addEnvRow}
                  >
                    <Plus className="h-3.5 w-3.5" />
                    Add variable
                  </Button>
                </div>
              </>
            )}

            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label>Target agents</Label>
                {agents.length > 0 && (
                  <button
                    type="button"
                    onClick={toggleAll}
                    className="text-xs text-muted-foreground hover:text-foreground"
                  >
                    {allSelected ? "Clear all" : "Select all"}
                  </button>
                )}
              </div>
              {agentsQuery.isLoading ? (
                <div className="space-y-2">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <Skeleton key={i} className="h-8 w-full" />
                  ))}
                </div>
              ) : agents.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No agents in this workspace yet.
                </p>
              ) : (
                <div className="rounded-md border border-border">
                  {agents.map((agent) => {
                    const redacted = agent.mcp_config_redacted === true;
                    return (
                      <label
                        key={agent.id}
                        className="flex items-center gap-3 border-b border-border px-3 py-2 last:border-0 has-disabled:opacity-50"
                      >
                        <Checkbox
                          checked={!!selected[agent.id]}
                          onCheckedChange={() => toggleAgent(agent.id)}
                          disabled={redacted}
                          aria-label={`Select ${agent.name}`}
                        />
                        <span className="flex-1 text-sm">{agent.name}</span>
                        {redacted && (
                          <span className="flex items-center gap-1 text-xs text-muted-foreground">
                            <Lock className="h-3 w-3" />
                            configured (hidden)
                          </span>
                        )}
                      </label>
                    );
                  })}
                </div>
              )}
            </div>

            <div className="flex items-center justify-end gap-3">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={resetForm}
                disabled={busy}
              >
                Reset
              </Button>
              <Button size="sm" onClick={addServer} disabled={!canSubmit}>
                {busy && <Loader2 className="mr-1 h-4 w-4 animate-spin" />}
                Add to{selectedIds.length ? ` ${selectedIds.length}` : ""} agent
                {selectedIds.length === 1 ? "" : "s"}
              </Button>
            </div>
          </div>
          )}
        </section>

        {/* ---------------- Per-agent server list ---------------- */}
        <h2 className="mb-2 text-sm font-medium">Configured servers</h2>
        {agentsQuery.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : (
          <div className="space-y-4">
            {agents.map((agent) => (
              <AgentServers
                key={agent.id}
                agent={agent}
                busy={busy}
                sealedAuth={sealedAuth}
                onRemove={(serverName) => deleteServer(agent, serverName)}
              />
            ))}
            {agents.length === 0 && (
              <div className="rounded-md border border-border px-3 py-6 text-center text-sm text-muted-foreground">
                No agents found.
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** One agent's row in the "Configured servers" list. */
function AgentServers({
  agent,
  busy,
  sealedAuth,
  onRemove,
}: {
  agent: Agent;
  busy: boolean;
  /** server name -> last4 for remote servers that have sealed auth configured. */
  sealedAuth: Map<string, string>;
  onRemove: (serverName: string) => void;
}) {
  const redacted = agent.mcp_config_redacted === true;
  const servers = useMemo(() => listMcpServers(agent.mcp_config), [agent.mcp_config]);

  return (
    <div className="rounded-md border border-border">
      <div className="flex items-center justify-between gap-2 border-b border-border bg-muted/30 px-3 py-2">
        <span className="text-sm font-medium">{agent.name}</span>
        {redacted ? (
          <span className="flex items-center gap-1 text-xs text-muted-foreground">
            <Lock className="h-3 w-3" />
            configured (hidden)
          </span>
        ) : (
          <span className="font-mono text-xs text-muted-foreground/70">
            {servers.length} server{servers.length === 1 ? "" : "s"}
          </span>
        )}
      </div>
      {redacted ? (
        <p className="px-3 py-3 text-xs text-muted-foreground">
          This agent has an MCP config, but it&apos;s hidden because it may
          contain secrets you&apos;re not permitted to view.
        </p>
      ) : servers.length === 0 ? (
        <p className="px-3 py-3 text-sm text-muted-foreground">
          No MCP servers configured.
        </p>
      ) : (
        servers.map((s) => {
          const isRemote = isRemoteMcpServer(s);
          const last4 = sealedAuth.get(s.name);
          return (
            <div
              key={s.name}
              className="flex items-start justify-between gap-3 border-b border-border px-3 py-2 last:border-0"
            >
              <div className="min-w-0 space-y-0.5">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium">{s.name}</span>
                  {isRemote ? (
                    <>
                      <span className="rounded bg-muted px-1 py-0.5 font-mono text-[10px] uppercase text-muted-foreground/70">
                        {s.type}
                      </span>
                      <code className="max-w-full truncate rounded bg-muted px-1 py-0.5 font-mono text-xs text-muted-foreground">
                        {s.url}
                      </code>
                    </>
                  ) : (
                    <code className="truncate rounded bg-muted px-1 py-0.5 font-mono text-xs text-muted-foreground">
                      {s.command}
                      {s.args?.length ? ` ${s.args.join(" ")}` : ""}
                    </code>
                  )}
                  {isRemote && sealedAuth.has(s.name) && (
                    <span className="flex items-center gap-1 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">
                      <ShieldCheck className="h-3 w-3" />
                      sealed auth{last4 ? ` ••${last4}` : ""}
                    </span>
                  )}
                </div>
                {isRemote
                  ? s.headers &&
                    Object.keys(s.headers).length > 0 && (
                      <div className="text-xs text-muted-foreground">
                        headers: {Object.keys(s.headers).join(", ")}
                      </div>
                    )
                  : s.env &&
                    Object.keys(s.env).length > 0 && (
                      <div className="text-xs text-muted-foreground">
                        env: {Object.keys(s.env).join(", ")}
                      </div>
                    )}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => onRemove(s.name)}
                disabled={busy}
                aria-label={`Remove ${s.name}`}
              >
                <Trash2 className="h-4 w-4 text-muted-foreground" />
              </Button>
            </div>
          );
        })
      )}
    </div>
  );
}

/** Dashboard page wrapper, re-exported as the route default by apps/web. */
export function McpPage() {
  return <McpServersPanel />;
}
