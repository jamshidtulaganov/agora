"use client";

import { LarkTab } from "./lark-tab";
import { BitrixTab } from "./bitrix-tab";
import { ZohoTab } from "./zoho-tab";
import { McpServersTab } from "./mcp-servers-tab";
import { FigmaIntegrationSection } from "./figma-integration-section";
import { ReleaseIntegrationsSection } from "./release-integrations-section";
import { WorkspaceDesignSection } from "./workspace-design-section";
import { useConfigStore } from "@agora/core/config";
import { useT } from "../../i18n";

// Integrations is the umbrella tab for third-party platform connections.
// GitHub has its own top-level tab (see github-tab.tsx); everything else
// lives here under its own section heading so additional integrations slot
// in without changing the IA. IntegrationsTab is just the host; each
// integration owns its own description and install flow.
//
// Bitrix / Zoho / Lark render only when the backend reports the integration
// as configured (capability flags on /api/config, mirroring the same env
// gates that enable those endpoints). A general dev-team deployment leaves
// them unset → this tab shows only the dev-native sections (Figma, Release,
// workspace design, MCP servers). The integration backends stay fully
// env-gated regardless — this only controls client-visible surface.
export function IntegrationsTab() {
  const { t } = useT("settings");
  const bitrixEnabled = useConfigStore((s) => s.bitrixEnabled);
  const zohoEnabled = useConfigStore((s) => s.zohoEnabled);
  const larkEnabled = useConfigStore((s) => s.larkEnabled);
  return (
    <div className="space-y-10">
      {larkEnabled && (
        <section className="space-y-4">
          <h2 className="text-sm font-semibold">{t(($) => $.lark.section_title)}</h2>
          <LarkTab />
        </section>
      )}
      {bitrixEnabled && <BitrixTab />}
      {zohoEnabled && <ZohoTab />}
      <FigmaIntegrationSection />
      <ReleaseIntegrationsSection />
      <WorkspaceDesignSection />
      <McpServersTab />
    </div>
  );
}
