/* eslint-disable i18next/no-literal-string -- Zoho integration card; i18n follow-up */
"use client";

import { DatabaseZap } from "lucide-react";
import { useWorkspacePaths } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { useNavigation } from "../../navigation";

/**
 * Zoho integration card in Settings → Integrations. Links to the import browser
 * (/[ws]/zoho) where an operator picks what to import per Zoho app (Projects,
 * Sprints — Desk/CRM to follow). Mirrors BitrixTab.
 */
export function ZohoTab() {
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  return (
    <section className="space-y-4">
      <h2 className="text-sm font-semibold">Zoho</h2>
      <div className="rounded-lg border border-border p-4">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <DatabaseZap className="h-4 w-4 text-muted-foreground" />
              <h3 className="text-sm font-medium">Zoho import</h3>
            </div>
            <p className="max-w-prose text-sm text-muted-foreground">
              Import work from the Zoho suite into Agora. Each Zoho Projects
              project (and Zoho Sprints project) becomes an Agora project; its
              tasks/items become issues. Desk tickets and CRM tasks are coming
              next.
            </p>
          </div>
          <Button size="sm" onClick={() => nav.push(paths.zoho())}>
            Open import
          </Button>
        </div>
      </div>
    </section>
  );
}
