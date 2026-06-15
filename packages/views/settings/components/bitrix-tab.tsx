/* eslint-disable i18next/no-literal-string -- Bitrix integration card; i18n follow-up */
"use client";

import { DatabaseZap } from "lucide-react";
import { useWorkspacePaths } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { useNavigation } from "../../navigation";

/**
 * Bitrix24 integration card in Settings → Integrations. Links to the import
 * browser (/[ws]/bitrix) where an operator selects workgroups to sync. Owns its
 * own section heading + copy, mirroring LarkTab.
 */
export function BitrixTab() {
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  return (
    <section className="space-y-4">
      <h2 className="text-sm font-semibold">Bitrix24</h2>
      <div className="rounded-lg border border-border p-4">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <DatabaseZap className="h-4 w-4 text-muted-foreground" />
              <h3 className="text-sm font-medium">Bitrix24 import</h3>
            </div>
            <p className="max-w-prose text-sm text-muted-foreground">
              Import Bitrix24 workgroups and tasks into Agora. Each group becomes
              a project; its tasks become issues with comments, attachments, and
              video frames. Issue status mirrors back to the Bitrix task.
            </p>
          </div>
          <Button size="sm" onClick={() => nav.push(paths.bitrix())}>
            Open import
          </Button>
        </div>
      </div>
    </section>
  );
}
