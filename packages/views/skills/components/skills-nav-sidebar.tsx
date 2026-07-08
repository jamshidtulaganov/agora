"use client";

import { Sparkles } from "lucide-react";
import type { SkillSummary } from "@agora/core/types";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import { skillListOptions } from "@agora/core/workspace/queries";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

// Master-detail navigation rail for the skill detail page: every workspace
// skill as a card, current one highlighted, click to switch without going
// back through the list page. Renders nothing until the (cached) skill list
// resolves — the detail page works fine without it.
export function SkillsNavSidebar({ currentSkillId }: { currentSkillId: string }) {
  const { t } = useT("skills");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { data: skills = [] } = useQuery(skillListOptions(wsId));

  if (skills.length === 0) return null;

  return (
    <nav
      aria-label={t(($) => $.page.title)}
      className="hidden w-64 shrink-0 flex-col border-r xl:flex"
    >
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-4">
        <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
          {t(($) => $.page.title)}
        </span>
        <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
          {skills.length}
        </span>
      </div>
      <ul className="flex-1 space-y-1 overflow-y-auto p-2">
        {skills.map((s: SkillSummary) => {
          const selected = s.id === currentSkillId;
          return (
            <li key={s.id}>
              <AppLink
                href={paths.skillDetail(s.id)}
                aria-current={selected ? "page" : undefined}
                className={cn(
                  "flex items-start gap-2.5 rounded-md px-2.5 py-2 transition-colors",
                  selected ? "bg-accent" : "hover:bg-accent/50",
                )}
              >
                <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border bg-background">
                  <Sparkles className="h-3.5 w-3.5 text-muted-foreground" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium text-foreground">
                    {s.name}
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {s.description || t(($) => $.table.no_description)}
                  </span>
                </span>
              </AppLink>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
