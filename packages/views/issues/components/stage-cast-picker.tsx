"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Wand2 } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { agentListOptions } from "@agora/core/workspace/queries";
import { useSetIssueMetadataKey, useDeleteIssueMetadataKey } from "@agora/core/issues/mutations";
import { ActorAvatar } from "../../common/actor-avatar";
import {
  PropertyPicker,
  PickerItem,
  PickerSection,
  PickerEmpty,
} from "./pickers/property-picker";
import { useT } from "../../i18n";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";

/**
 * StageCastPicker pins a single agent to a stage of an issue's pipeline (the
 * orchestrator's per-task casting, human-overridable). It writes/clears an
 * issue-metadata key — the same one maybeRunQAOnInReview / resolveReviewerAgent
 * read on the backend. Unset === "Auto": the workspace default roster picks.
 */
export function StageCastPicker({
  issueId,
  stageKey,
  agentId,
  align = "start",
}: {
  issueId: string;
  stageKey: string;
  agentId: string | null | undefined;
  align?: "start" | "center" | "end";
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const setCast = useSetIssueMetadataKey();
  const clearCast = useDeleteIssueMetadataKey();

  const activeAgents = useMemo(() => agents.filter((a) => !a.archived_at), [agents]);
  const selected = agentId ? activeAgents.find((a) => a.id === agentId) : undefined;

  const query = filter.trim().toLowerCase();
  const filtered = activeAgents.filter(
    (a) => !query || a.name.toLowerCase().includes(query) || matchesPinyin(a.name, query),
  );

  const pick = (id: string) => {
    setCast.mutate({ issueId, key: stageKey, value: id });
    setOpen(false);
  };
  const clearToAuto = () => {
    clearCast.mutate({ issueId, key: stageKey });
    setOpen(false);
  };

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-56"
      align={align}
      searchable
      searchPlaceholder={t(($) => $.detail.cast_filter_placeholder)}
      onSearchChange={setFilter}
      trigger={
        selected ? (
          <>
            <ActorAvatar actorType="agent" actorId={selected.id} size={16} showStatusDot />
            <span className="truncate">{selected.name}</span>
          </>
        ) : (
          <span className="flex items-center gap-1.5 text-muted-foreground">
            <Wand2 className="size-3" />
            {t(($) => $.detail.cast_auto)}
          </span>
        )
      }
    >
      <PickerItem selected={!agentId} onClick={clearToAuto}>
        <Wand2 className="size-3.5 text-muted-foreground" />
        <span className="truncate">{t(($) => $.detail.cast_auto_full)}</span>
      </PickerItem>
      {filtered.length === 0 ? (
        <PickerEmpty />
      ) : (
        <PickerSection label={t(($) => $.detail.cast_agents_group)}>
          {filtered.map((a) => (
            <PickerItem key={a.id} selected={a.id === agentId} onClick={() => pick(a.id)}>
              <ActorAvatar actorType="agent" actorId={a.id} size={16} showStatusDot />
              <span className="truncate">{a.name}</span>
            </PickerItem>
          ))}
        </PickerSection>
      )}
    </PropertyPicker>
  );
}
