"use client";

import { useWorkspacePaths } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

/**
 * MCP servers card body in Settings → Integrations. Links to the
 * workspace-level MCP admin page (/[ws]/mcp) where an operator reviews each
 * agent's configured MCP servers and adds new ones across agents. The card
 * chrome (icon, name, one-line description) is provided by IntegrationCard;
 * this renders only the launcher body.
 */
export function McpServersTab() {
  const { t } = useT("settings");
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  return (
    <div className="flex items-start justify-between gap-4">
      <p className="max-w-prose text-sm text-muted-foreground">
        {t(($) => $.integrations.mcp.body)}
      </p>
      <Button size="sm" className="shrink-0" onClick={() => nav.push(paths.mcp())}>
        {t(($) => $.integrations.mcp.open)}
      </Button>
    </div>
  );
}
