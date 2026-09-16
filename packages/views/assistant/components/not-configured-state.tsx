"use client";

import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import {
  Empty,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
  EmptyDescription,
} from "@agora/ui/components/ui/empty";
import { useT } from "../../i18n";

/**
 * Rendered instead of the chat UI when `GET /api/assistant/availability`
 * reports `enabled: false` — the feature is switched off or unkeyed on this
 * instance. Mirrors the SummarizeComments 503 pattern (CLAUDE.md's
 * per-endpoint availability gate).
 */
export function AssistantNotConfiguredState() {
  const { t } = useT("assistant");

  return (
    <div className="flex h-full items-center justify-center">
      <Empty className="max-w-md border-none">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <AgoraIcon noSpin className="size-full" />
          </EmptyMedia>
          <EmptyTitle>{t(($) => $.not_configured.title)}</EmptyTitle>
          <EmptyDescription>{t(($) => $.not_configured.description)}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    </div>
  );
}
