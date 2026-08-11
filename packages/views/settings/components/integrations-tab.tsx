"use client";

import { DatabaseZap, MessageSquare, Palette, Plug, Rocket, Send } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { useConfigStore } from "@agora/core/config";
import { useWorkspaceId } from "@agora/core/hooks";
import { zohoConnectionOptions } from "@agora/core/zoho";
import { larkInstallationsOptions } from "@agora/core/lark";
import { telegramInstallationsOptions } from "@agora/core/telegram";
import { LarkTab } from "./lark-tab";
import { TelegramTab } from "./telegram-tab";
import { BitrixTab } from "./bitrix-tab";
import { ZohoTab } from "./zoho-tab";
import { McpServersTab } from "./mcp-servers-tab";
import { FigmaIntegrationSection } from "./figma-integration-section";
import { ReleaseIntegrationsSection } from "./release-integrations-section";
import { IntegrationCard } from "./integration-card";
import { useT } from "../../i18n";

// Integrations is a collapsible connector gallery: one IntegrationCard per
// third-party platform, each showing its connection status and expanding to
// its configure form. GitHub keeps its own top-level tab (github-tab.tsx);
// everything else lives here. Dev-native connectors (MCP, Release, Figma) come
// first; Bitrix / Zoho / Lark render only when the backend reports the
// integration as configured (capability flags on /api/config, mirroring the
// env gates that enable those endpoints). A general dev-team deployment leaves
// them unset → this tab shows only the dev-native cards. The integration
// backends stay fully env-gated regardless — this only controls client surface.
//
// The status badge on each card comes from a lightweight probe query keyed
// identically to the one inside the section body, so TanStack dedupes and no
// extra request is made. A collapsed card never mounts its section body (and
// so never runs the body's own queries) — only the status probe runs.
export function IntegrationsTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const bitrixEnabled = useConfigStore((s) => s.bitrixEnabled);
  const zohoEnabled = useConfigStore((s) => s.zohoEnabled);
  const larkEnabled = useConfigStore((s) => s.larkEnabled);

  const { data: figmaStatus } = useQuery({
    queryKey: ["figma-credential", wsId],
    queryFn: () => api.getFigmaCredentialStatus(wsId),
    enabled: !!wsId,
  });
  const { data: releaseIntegrations } = useQuery({
    queryKey: ["release-integrations", wsId],
    queryFn: () => api.listReleaseIntegrations(wsId),
    enabled: !!wsId,
  });
  const { data: zohoConnection } = useQuery({
    ...zohoConnectionOptions(wsId),
    enabled: !!wsId && zohoEnabled,
  });
  const { data: larkData } = useQuery({
    ...larkInstallationsOptions(wsId),
    enabled: !!wsId && larkEnabled,
  });
  // Same options factory the tab body spreads, so the card header and the
  // panel share one cache entry instead of fetching twice.
  const { data: telegramData } = useQuery({
    ...telegramInstallationsOptions(wsId),
    enabled: !!wsId,
  });

  const status = (connected: boolean): "connected" | "not_connected" =>
    connected ? "connected" : "not_connected";

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t(($) => $.integrations.intro)}</p>

      <div className="space-y-3">
        <IntegrationCard
          icon={<Plug className="h-4 w-4" />}
          name={t(($) => $.integrations.mcp.name)}
          description={t(($) => $.integrations.mcp.description)}
        >
          <McpServersTab />
        </IntegrationCard>

        <IntegrationCard
          icon={<Rocket className="h-4 w-4" />}
          name={t(($) => $.integrations.release.name)}
          description={t(($) => $.integrations.release.description)}
          status={status((releaseIntegrations?.length ?? 0) > 0)}
        >
          <ReleaseIntegrationsSection />
        </IntegrationCard>

        <IntegrationCard
          icon={<Palette className="h-4 w-4" />}
          name={t(($) => $.integrations.figma.name)}
          description={t(($) => $.integrations.figma.description)}
          status={status(figmaStatus?.configured === true)}
        >
          <FigmaIntegrationSection />
        </IntegrationCard>

        {bitrixEnabled ? (
          <IntegrationCard
            icon={<DatabaseZap className="h-4 w-4" />}
            name={t(($) => $.integrations.bitrix.name)}
            description={t(($) => $.integrations.bitrix.description)}
          >
            <BitrixTab />
          </IntegrationCard>
        ) : null}

        {zohoEnabled ? (
          <IntegrationCard
            icon={<DatabaseZap className="h-4 w-4" />}
            name={t(($) => $.integrations.zoho.name)}
            description={t(($) => $.integrations.zoho.description)}
            status={status(zohoConnection?.configured === true)}
          >
            <ZohoTab />
          </IntegrationCard>
        ) : null}

        <IntegrationCard
          icon={<Send className="h-4 w-4" />}
          name={t(($) => $.integrations.telegram.name)}
          description={t(($) => $.integrations.telegram.description)}
          // `configured` only means the server has a seal key and can accept
          // bot tokens. Calling that Connected produced a green badge beside
          // an empty "No bots connected" panel. Connection state is the
          // workspace's active per-agent installations instead.
          status={status(
            (telegramData?.installations ?? []).some(
              (installation) => installation.status === "active",
            ),
          )}
        >
          <TelegramTab />
        </IntegrationCard>

        {larkEnabled ? (
          <IntegrationCard
            icon={<MessageSquare className="h-4 w-4" />}
            name={t(($) => $.integrations.lark.name)}
            description={t(($) => $.integrations.lark.description)}
            status={status(larkData?.configured === true)}
          >
            <LarkTab />
          </IntegrationCard>
        ) : null}
      </div>
    </div>
  );
}
