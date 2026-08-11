/* eslint-disable i18next/no-literal-string -- Bitrix integration card; i18n follow-up */
"use client";

import { useWorkspacePaths } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { useNavigation } from "../../navigation";

/**
 * Bitrix24 integration card body in Settings → Integrations. Links to the
 * import browser (/[ws]/bitrix) where an operator selects workgroups to sync.
 * The card chrome (icon, name, one-line description) is provided by
 * IntegrationCard; this renders only the body.
 */
export function BitrixTab() {
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  return (
    <div className="flex items-start justify-between gap-4">
      <p className="max-w-prose text-sm text-muted-foreground">
        Personal Bitrix24 mirror: tasks become issues with comments,
        attachments, and video frames. Bitrix stays source of truth; only your
        human comments and attachments sync outbound.
      </p>
      <Button size="sm" className="shrink-0" onClick={() => nav.push(paths.bitrix())}>
        Open import
      </Button>
    </div>
  );
}
