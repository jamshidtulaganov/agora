/* eslint-disable i18next/no-literal-string -- internal Plugins admin panel; i18n is a follow-up */
"use client";

import { useMemo, useState } from "react";
import { Boxes, ChevronDown, Loader2, Plug, Plus, Trash2, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import {
  pluginListOptions,
  agentListOptions,
  skillListOptions,
  useCreatePlugin,
  useDeletePlugin,
  useInstallPlugin,
  useUninstallPlugin,
  type Plugin,
} from "@agora/core/plugins";
import {
  buildServerEntry,
  listMcpServers,
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
import { AppLink } from "../../navigation";

interface EnvRow {
  key: string;
  value: string;
}

/**
 * Workspace-level Plugins admin. A plugin bundles workspace skills + MCP
 * connectors and installs them onto agents as a unit. Lists existing plugins
 * (skills, connectors, install state) with a per-plugin install/uninstall
 * control, plus a "Create plugin" form (name, description, skill multi-select,
 * optional connectors editor). Mirrors the MCP servers panel's structure
 * (PageHeader + flat scrolling body, hardcoded English copy). Drives the
 * `/api/plugins` endpoints via the plugins mutations.
 */
export function PluginsPanel() {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const pluginsQuery = useQuery(pluginListOptions(wsId));
  const agentsQuery = useQuery(agentListOptions(wsId));
  const skillsQuery = useQuery(skillListOptions(wsId));
  const createMut = useCreatePlugin(wsId);
  const deleteMut = useDeletePlugin(wsId);

  const plugins = pluginsQuery.data ?? [];
  const agents = useMemo(
    () => (agentsQuery.data ?? []).filter((a) => !a.archived_at),
    [agentsQuery.data],
  );
  const skills = useMemo(
    () =>
      (skillsQuery.data ?? [])
        .slice()
        .sort((a, b) => a.name.localeCompare(b.name)),
    [skillsQuery.data],
  );

  // --- Create-plugin form state ---
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [selectedSkills, setSelectedSkills] = useState<Record<string, boolean>>(
    {},
  );
  // Connectors editor — same name/command/args/env shape as the MCP panel.
  const [serverName, setServerName] = useState("");
  const [command, setCommand] = useState("");
  const [argsText, setArgsText] = useState("");
  const [env, setEnv] = useState<EnvRow[]>([{ key: "", value: "" }]);

  const selectedSkillIds = Object.keys(selectedSkills).filter(
    (id) => selectedSkills[id],
  );

  function toggleSkill(id: string) {
    setSelectedSkills((s) => ({ ...s, [id]: !s[id] }));
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
    setServerName(tpl.name);
    // Plugins bundle a local-command (stdio) MCP server; remote templates are
    // filtered out below, so the stdio fields are the only ones used here.
    setCommand(tpl.command ?? "");
    setArgsText(tpl.argsText ?? "");
    setEnv(
      tpl.envKeys?.length
        ? tpl.envKeys.map((key) => ({ key, value: "" }))
        : [{ key: "", value: "" }],
    );
  }

  function resetForm() {
    setName("");
    setDescription("");
    setSelectedSkills({});
    setServerName("");
    setCommand("");
    setArgsText("");
    setEnv([{ key: "", value: "" }]);
  }

  // A connector is included only when both a server name and command are set;
  // a half-filled row is treated as "no connector" rather than an error.
  const hasConnector = serverName.trim().length > 0 && command.trim().length > 0;

  const canSubmit =
    name.trim().length > 0 &&
    (selectedSkillIds.length > 0 || hasConnector) &&
    !createMut.isPending;

  async function createPlugin() {
    const trimmedName = name.trim();
    if (!trimmedName) return;
    if (!selectedSkillIds.length && !hasConnector) return;

    const mcp_config = hasConnector
      ? {
          mcpServers: {
            [serverName.trim()]: buildServerEntry({ command, argsText, env }),
          },
        }
      : null;

    try {
      await createMut.mutateAsync({
        name: trimmedName,
        description: description.trim() || undefined,
        mcp_config,
        skill_ids: selectedSkillIds,
      });
      toast.success(`Created plugin "${trimmedName}".`);
      resetForm();
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to create plugin",
      );
    }
  }

  async function deletePlugin(plugin: Plugin) {
    try {
      await deleteMut.mutateAsync(plugin.id);
      toast.success(`Deleted plugin "${plugin.name}".`);
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to delete plugin",
      );
    }
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <Boxes className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">Plugins</h1>
          {plugins.length > 0 && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
              {plugins.length}
            </span>
          )}
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto p-5">
        <p className="mb-4 text-sm text-muted-foreground">
          A plugin bundles workspace skills and MCP connectors and installs them
          onto agents as a unit. Installing a plugin binds its skills and merges
          its connectors into each agent&apos;s{" "}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
            mcp_config
          </code>{" "}
          — preserving everything the agent already has.
        </p>

        <div className="mb-6 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/15 p-4">
          <div className="flex min-w-0 items-start gap-3">
            <Plug className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <div>
              <p className="text-sm font-medium">Need one direct connection?</p>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                MCP servers connect tools or data sources directly. Plugins are
                reusable bundles that can include skills and MCP connectors.
              </p>
            </div>
          </div>
          <Button
            variant="outline"
            size="sm"
            render={<AppLink href={paths.mcp()} />}
          >
            Configure MCP servers
          </Button>
        </div>

        {pluginsQuery.isError && (
          <div className="mb-4 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            Failed to load plugins — {(pluginsQuery.error as Error)?.message}
          </div>
        )}

        {/* ---------------- Create plugin form ---------------- */}
        <section className="mb-8 rounded-md border border-border">
          <div className="flex items-center gap-2 border-b border-border bg-muted/30 px-3 py-2 text-xs font-medium text-muted-foreground">
            <Plus className="h-3.5 w-3.5" />
            Create plugin
          </div>
          <div className="space-y-4 p-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="plugin-name">Name</Label>
                <Input
                  id="plugin-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Design"
                  autoComplete="off"
                  spellCheck={false}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="plugin-description">Description</Label>
                <Input
                  id="plugin-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Skills and connectors for design work"
                  autoComplete="off"
                  spellCheck={false}
                />
              </div>
            </div>

            {/* Skills multi-select */}
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label>Skills</Label>
                {selectedSkillIds.length > 0 && (
                  <span className="text-xs text-muted-foreground">
                    {selectedSkillIds.length} selected
                  </span>
                )}
              </div>
              {skillsQuery.isLoading ? (
                <div className="space-y-2">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <Skeleton key={i} className="h-8 w-full" />
                  ))}
                </div>
              ) : skills.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No skills in this workspace yet.
                </p>
              ) : (
                <div className="max-h-56 overflow-auto rounded-md border border-border">
                  {skills.map((skill) => (
                    <label
                      key={skill.id}
                      className="flex items-start gap-3 border-b border-border px-3 py-2 last:border-0"
                    >
                      <Checkbox
                        checked={!!selectedSkills[skill.id]}
                        onCheckedChange={() => toggleSkill(skill.id)}
                        aria-label={`Select ${skill.name}`}
                      />
                      <span className="min-w-0 space-y-0.5">
                        <span className="block text-sm">{skill.name}</span>
                        {skill.description && (
                          <span className="block truncate text-xs text-muted-foreground">
                            {skill.description}
                          </span>
                        )}
                      </span>
                    </label>
                  ))}
                </div>
              )}
            </div>

            {/* Connector editor (optional) */}
            <div className="space-y-3 rounded-md border border-border bg-muted/20 p-3">
              <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
                Connector (optional)
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs text-muted-foreground">
                  Quick templates:
                </span>
                {MCP_SERVER_TEMPLATES.filter(
                  (tpl) => (tpl.transport ?? "stdio") === "stdio",
                ).map((tpl) => (
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
                  <Label htmlFor="plugin-server-name">Server name</Label>
                  <Input
                    id="plugin-server-name"
                    value={serverName}
                    onChange={(e) => setServerName(e.target.value)}
                    placeholder="figma"
                    autoComplete="off"
                    spellCheck={false}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="plugin-command">Command</Label>
                  <Input
                    id="plugin-command"
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
                <Label htmlFor="plugin-args">Args</Label>
                <Input
                  id="plugin-args"
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
            </div>

            <div className="flex items-center justify-end gap-3">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={resetForm}
                disabled={createMut.isPending}
              >
                Reset
              </Button>
              <Button size="sm" onClick={createPlugin} disabled={!canSubmit}>
                {createMut.isPending && (
                  <Loader2 className="mr-1 h-4 w-4 animate-spin" />
                )}
                Create plugin
              </Button>
            </div>
          </div>
        </section>

        {/* ---------------- Existing plugins ---------------- */}
        <h2 className="mb-2 text-sm font-medium">Plugins</h2>
        {pluginsQuery.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-24 w-full" />
            ))}
          </div>
        ) : (
          <div className="space-y-4">
            {plugins.map((plugin) => (
              <PluginCard
                key={plugin.id}
                plugin={plugin}
                agents={agents}
                agentsLoading={agentsQuery.isLoading}
                busy={deleteMut.isPending}
                onDelete={() => deletePlugin(plugin)}
              />
            ))}
            {plugins.length === 0 && (
              <div className="rounded-md border border-border px-3 py-6 text-center text-sm text-muted-foreground">
                No plugins yet. Create one above.
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** One plugin's card: skills, connectors, and the install/uninstall control. */
function PluginCard({
  plugin,
  agents,
  agentsLoading,
  busy,
  onDelete,
}: {
  plugin: Plugin;
  agents: Agent[];
  agentsLoading: boolean;
  busy: boolean;
  onDelete: () => void;
}) {
  const wsId = useWorkspaceId();
  const installMut = useInstallPlugin(wsId);
  const uninstallMut = useUninstallPlugin(wsId);
  const [open, setOpen] = useState(false);

  // Server (connector) names parsed off the redacted mcp_config blob.
  const connectors = useMemo(
    () => listMcpServers(plugin.mcp_config),
    [plugin.mcp_config],
  );
  const installedSet = useMemo(
    () => new Set(plugin.installed_agent_ids),
    [plugin.installed_agent_ids],
  );
  const mutating = installMut.isPending || uninstallMut.isPending;

  async function toggleAgent(agent: Agent, installed: boolean) {
    try {
      if (installed) {
        await uninstallMut.mutateAsync({
          pluginId: plugin.id,
          agentIds: [agent.id],
        });
        toast.success(`Uninstalled "${plugin.name}" from ${agent.name}.`);
      } else {
        await installMut.mutateAsync({
          pluginId: plugin.id,
          agentIds: [agent.id],
        });
        toast.success(`Installed "${plugin.name}" on ${agent.name}.`);
      }
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : `Failed to update ${agent.name}`,
      );
    }
  }

  return (
    <div className="rounded-md border border-border">
      <div className="flex items-start justify-between gap-3 border-b border-border bg-muted/30 px-3 py-2">
        <div className="min-w-0 space-y-0.5">
          <div className="text-sm font-medium">{plugin.name}</div>
          {plugin.description && (
            <div className="text-xs text-muted-foreground">
              {plugin.description}
            </div>
          )}
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={onDelete}
          disabled={busy || mutating}
          aria-label={`Delete ${plugin.name}`}
        >
          <Trash2 className="h-4 w-4 text-muted-foreground" />
        </Button>
      </div>

      <div className="space-y-3 px-3 py-3">
        {/* Skills */}
        <div className="space-y-1">
          <div className="text-xs font-medium text-muted-foreground">Skills</div>
          {plugin.skills.length === 0 ? (
            <div className="text-xs text-muted-foreground">No skills.</div>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {plugin.skills.map((skill) => (
                <span
                  key={skill.id}
                  className="rounded bg-muted px-1.5 py-0.5 text-xs"
                >
                  {skill.name}
                </span>
              ))}
            </div>
          )}
        </div>

        {/* Connectors */}
        <div className="space-y-1">
          <div className="text-xs font-medium text-muted-foreground">
            Connectors
          </div>
          {connectors.length === 0 ? (
            <div className="text-xs text-muted-foreground">No connectors.</div>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {connectors.map((c) => (
                <span
                  key={c.name}
                  className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                >
                  {c.name}
                </span>
              ))}
            </div>
          )}
        </div>

        {/* Install control */}
        <div className="space-y-2">
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
          >
            <ChevronDown
              className={`h-3.5 w-3.5 transition-transform ${open ? "" : "-rotate-90"}`}
            />
            Installed on {plugin.installed_agent_ids.length} agent
            {plugin.installed_agent_ids.length === 1 ? "" : "s"}
          </button>
          {open &&
            (agentsLoading ? (
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
                  const installed = installedSet.has(agent.id);
                  return (
                    <label
                      key={agent.id}
                      className="flex items-center gap-3 border-b border-border px-3 py-2 last:border-0"
                    >
                      <Checkbox
                        checked={installed}
                        onCheckedChange={() => toggleAgent(agent, installed)}
                        disabled={mutating}
                        aria-label={`Toggle ${plugin.name} on ${agent.name}`}
                      />
                      <span className="flex-1 text-sm">{agent.name}</span>
                      {installed && (
                        <span className="text-xs text-muted-foreground">
                          installed
                        </span>
                      )}
                    </label>
                  );
                })}
              </div>
            ))}
        </div>
      </div>
    </div>
  );
}

/** Dashboard page wrapper, re-exported as the route default by apps/web. */
export function PluginsPage() {
  return <PluginsPanel />;
}
