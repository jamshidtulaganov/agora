/* eslint-disable i18next/no-literal-string -- internal MCP-servers admin panel; i18n is a follow-up */
"use client";

import { useMemo, useState } from "react";
import { Loader2, Lock, Plug, Plus, Trash2, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  agentListOptions,
  useUpdateAgentMcpConfig,
  buildServerEntry,
  listMcpServers,
  removeMcpServer,
  upsertMcpServer,
  MCP_SERVER_TEMPLATES,
  type McpServerTemplate,
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
  const updateMut = useUpdateAgentMcpConfig(wsId);

  const agents = useMemo(
    () => (agentsQuery.data ?? []).filter((a) => !a.archived_at),
    [agentsQuery.data],
  );

  // --- Add-server form state ---
  const [name, setName] = useState("");
  const [command, setCommand] = useState("");
  const [argsText, setArgsText] = useState("");
  const [env, setEnv] = useState<EnvRow[]>([{ key: "", value: "" }]);
  const [selected, setSelected] = useState<Record<string, boolean>>({});

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
    setCommand(tpl.command);
    setArgsText(tpl.argsText);
    setEnv(
      tpl.envKeys.length
        ? tpl.envKeys.map((key) => ({ key, value: "" }))
        : [{ key: "", value: "" }],
    );
  }

  function resetForm() {
    setName("");
    setCommand("");
    setArgsText("");
    setEnv([{ key: "", value: "" }]);
    setSelected({});
  }

  const canSubmit =
    name.trim().length > 0 &&
    command.trim().length > 0 &&
    selectedIds.length > 0 &&
    !updateMut.isPending;

  async function addServer() {
    const trimmedName = name.trim();
    if (!trimmedName || !command.trim() || !selectedIds.length) return;
    const entry = buildServerEntry({ command, argsText, env });

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
    if (ok > 0) {
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
          <div className="flex items-center gap-2 border-b border-border bg-muted/30 px-3 py-2 text-xs font-medium text-muted-foreground">
            <Plus className="h-3.5 w-3.5" />
            Add MCP server
          </div>
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

            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="mcp-name">Server name</Label>
                <Input
                  id="mcp-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="figma"
                  autoComplete="off"
                  spellCheck={false}
                />
              </div>
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
                disabled={updateMut.isPending}
              >
                Reset
              </Button>
              <Button size="sm" onClick={addServer} disabled={!canSubmit}>
                {updateMut.isPending && (
                  <Loader2 className="mr-1 h-4 w-4 animate-spin" />
                )}
                Add to{selectedIds.length ? ` ${selectedIds.length}` : ""} agent
                {selectedIds.length === 1 ? "" : "s"}
              </Button>
            </div>
          </div>
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
                busy={updateMut.isPending}
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
  onRemove,
}: {
  agent: Agent;
  busy: boolean;
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
        servers.map((s) => (
          <div
            key={s.name}
            className="flex items-start justify-between gap-3 border-b border-border px-3 py-2 last:border-0"
          >
            <div className="min-w-0 space-y-0.5">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">{s.name}</span>
                <code className="truncate rounded bg-muted px-1 py-0.5 font-mono text-xs text-muted-foreground">
                  {s.command}
                  {s.args?.length ? ` ${s.args.join(" ")}` : ""}
                </code>
              </div>
              {s.env && Object.keys(s.env).length > 0 && (
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
        ))
      )}
    </div>
  );
}

/** Dashboard page wrapper, re-exported as the route default by apps/web. */
export function McpPage() {
  return <McpServersPanel />;
}
