"use client";

import {
  Download,
  HardDrive,
  Lock,
  Pencil,
  Sparkles,
} from "lucide-react";
import type {
  Agent,
  AgentRuntime,
  MemberWithUser,
  SkillSummary,
} from "@agora/core/types";
import { ActorAvatar } from "@agora/ui/components/common/actor-avatar";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@agora/ui/components/ui/tooltip";
import { resolvePublicFileUrl } from "@agora/core/workspace/avatar-url";
import { readOrigin } from "../lib/origin";
import { AppLink } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";

// Per-card data assembled at the page level. `skill` is the list-shape
// `SkillSummary`; the body and files are loaded only when the user opens the
// detail page.
export interface SkillRow {
  skill: SkillSummary;
  agents: Agent[];
  creator: MemberWithUser | null;
  // Originating runtime when the skill was imported from a runtime-local
  // store; null for manually-created or remotely-sourced skills.
  runtime: AgentRuntime | null;
  canEdit: boolean;
}

function AgentAssignees({ agents }: { agents: Agent[] }) {
  const { t } = useT("skills");
  if (agents.length === 0) {
    return (
      <span className="text-xs text-muted-foreground/70">
        {t(($) => $.table.unused)}
      </span>
    );
  }
  const visible = agents.slice(0, 3);
  const extra = agents.length - visible.length;
  return (
    <div className="flex items-center -space-x-1.5">
      {visible.map((a) => (
        <Tooltip key={a.id}>
          <TooltipTrigger
            render={
              <span className="inline-flex rounded-full ring-2 ring-background">
                <ActorAvatar
                  name={a.name}
                  initials={a.name.slice(0, 2).toUpperCase()}
                  avatarUrl={resolvePublicFileUrl(a.avatar_url)}
                  isAgent
                  size={22}
                />
              </span>
            }
          />
          <TooltipContent>{a.name}</TooltipContent>
        </Tooltip>
      ))}
      {extra > 0 && (
        <span className="inline-flex h-6 w-6 items-center justify-center rounded-full bg-muted text-xs font-medium text-muted-foreground ring-2 ring-background">
          +{extra}
        </span>
      )}
    </div>
  );
}

function SourceLine({
  skill,
  creator,
  runtime,
}: {
  skill: SkillSummary;
  creator: MemberWithUser | null;
  runtime: AgentRuntime | null;
}) {
  const { t } = useT("skills");
  const origin = readOrigin(skill);

  let icon = <Pencil className="h-3 w-3 shrink-0" />;
  let label: string = t(($) => $.table.source_manual);
  if (origin.type === "runtime_local") {
    icon = <HardDrive className="h-3 w-3 shrink-0" />;
    label = runtime
      ? t(($) => $.table.source_runtime_named, { name: runtime.name })
      : origin.provider
        ? t(($) => $.table.source_runtime_provider, { provider: origin.provider })
        : t(($) => $.table.source_runtime_unknown);
  } else if (origin.type === "clawhub") {
    icon = <Download className="h-3 w-3 shrink-0" />;
    label = t(($) => $.table.source_clawhub);
  } else if (origin.type === "skills_sh") {
    icon = <Download className="h-3 w-3 shrink-0" />;
    label = t(($) => $.table.source_skills_sh);
  } else if (origin.type === "github") {
    icon = <Download className="h-3 w-3 shrink-0" />;
    label = t(($) => $.table.source_github);
  }

  return (
    <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
      <span className="shrink-0">{icon}</span>
      <span className="block min-w-0 truncate">
        {label}
        {creator &&
          ` · ${t(($) => $.table.by_creator, { name: creator.name })}`}
      </span>
    </div>
  );
}

export function SkillCard({ row, href }: { row: SkillRow; href: string }) {
  const { t } = useT("skills");
  const timeAgo = useTimeAgo();
  const { skill, agents, creator, runtime, canEdit } = row;

  return (
    <AppLink
      href={href}
      className="group flex flex-col gap-2 rounded-lg border bg-card p-4 transition-colors hover:border-muted-foreground/30 hover:bg-accent/40"
    >
      <div className="flex items-start gap-2.5">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border bg-background">
          <Sparkles className="h-4 w-4 text-muted-foreground" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="block min-w-0 truncate text-sm font-medium">
              {skill.name}
            </span>
            {!canEdit && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Lock className="h-3 w-3 shrink-0 text-muted-foreground/60" />
                  }
                />
                <TooltipContent>{t(($) => $.table.lock_tooltip)}</TooltipContent>
              </Tooltip>
            )}
          </div>
          <div
            className={`mt-0.5 line-clamp-2 text-xs ${
              skill.description
                ? "text-muted-foreground"
                : "italic text-muted-foreground/50"
            }`}
          >
            {skill.description || t(($) => $.table.no_description)}
          </div>
        </div>
      </div>
      <div className="mt-auto flex items-center justify-between gap-2 border-t pt-2">
        <AgentAssignees agents={agents} />
        <div className="flex min-w-0 items-center gap-2">
          <SourceLine skill={skill} creator={creator} runtime={runtime} />
          <span className="shrink-0 whitespace-nowrap text-xs text-muted-foreground/70">
            {timeAgo(skill.updated_at)}
          </span>
        </div>
      </div>
    </AppLink>
  );
}
