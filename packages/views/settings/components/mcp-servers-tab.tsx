/* eslint-disable i18next/no-literal-string -- MCP servers integration card; i18n follow-up */
"use client";

import { Plug } from "lucide-react";
import { useWorkspacePaths } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { useNavigation } from "../../navigation";

/**
 * MCP servers card in Settings → Integrations. Links to the workspace-level
 * MCP admin page (/[ws]/mcp) where an operator reviews each agent's configured
 * MCP servers and adds new ones across agents. Mirrors BitrixTab.
 */
export function McpServersTab() {
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  return (
    <section className="space-y-4">
      <h2 className="text-sm font-semibold">MCP servers</h2>
      <div className="rounded-lg border border-border p-4">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <Plug className="h-4 w-4 text-muted-foreground" />
              <h3 className="text-sm font-medium">MCP servers</h3>
            </div>
            <p className="max-w-prose text-sm text-muted-foreground">
              Review which Model Context Protocol servers each agent has
              configured, and add new servers (Figma, GitHub, MySQL, or a custom
              command) across one or more agents at once.
            </p>
          </div>
          <Button size="sm" onClick={() => nav.push(paths.mcp())}>
            Open MCP servers
          </Button>
        </div>
      </div>
    </section>
  );
}
