"use client";

import { type ReactNode, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Badge } from "@agora/ui/components/ui/badge";
import { useT } from "../../i18n";

export interface IntegrationCardProps {
  /** Small leading glyph (e.g. a lucide icon). Rendered muted in the header. */
  icon: ReactNode;
  /** Connector name — the header title. */
  name: string;
  /** One-line summary shown muted under the name; longer help goes in the body. */
  description: string;
  /**
   * Optional connection status for the header badge. Omit for launcher-style
   * cards that have no per-workspace connection state (MCP, Bitrix import).
   */
  status?: "connected" | "not_connected";
  /** Start expanded. Defaults to collapsed for a scannable gallery. */
  defaultOpen?: boolean;
  /** The form/body revealed when the card is expanded. */
  children: ReactNode;
}

/**
 * A single connector row in the Settings → Integrations gallery. The whole
 * header is one focusable toggle button (icon + name + one-line description +
 * optional status badge + chevron); the body mounts only while expanded, so a
 * collapsed card never runs its section's queries. Collapse pattern mirrors
 * the MCP servers panel's "Add server" toggle.
 */
export function IntegrationCard({
  icon,
  name,
  description,
  status,
  defaultOpen = false,
  children,
}: IntegrationCardProps) {
  const [open, setOpen] = useState(defaultOpen);
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <Card className="gap-0 py-0">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50"
      >
        <span className="shrink-0 text-muted-foreground">{icon}</span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{name}</span>
          <span className="block truncate text-xs text-muted-foreground">
            {description}
          </span>
        </span>
        {status ? <IntegrationStatusBadge status={status} /> : null}
        <Chevron className="h-4 w-4 shrink-0 text-muted-foreground" />
      </button>
      {open ? (
        <CardContent className="border-t border-foreground/10 pt-4 pb-4">
          {children}
        </CardContent>
      ) : null}
    </Card>
  );
}

function IntegrationStatusBadge({
  status,
}: {
  status: "connected" | "not_connected";
}) {
  const { t } = useT("settings");
  if (status === "connected") {
    return (
      <Badge
        variant="outline"
        className="shrink-0 border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
      >
        {t(($) => $.integrations.status_connected)}
      </Badge>
    );
  }
  return (
    <Badge variant="outline" className="shrink-0 text-muted-foreground">
      {t(($) => $.integrations.status_not_connected)}
    </Badge>
  );
}
