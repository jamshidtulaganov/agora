"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useConfigStore } from "@agora/core/config";
import { useBeginSlackUserLink } from "@agora/core/slack";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { useT } from "../../i18n";

// The same cache key telegram-notification-setting.tsx uses: both rows read
// one /api/me/links response, so connecting either refreshes both.
const MY_LINKS_KEY = ["me", "external-links"] as const;

/**
 * Personal Slack connection.
 *
 * Two things depend on it, and neither is configurable here:
 *
 *   - Inbox DMs. They hang off `inbox:new`, and a muted notification never
 *     creates an inbox item — so the preferences above this row already govern
 *     Slack, with no second surface to keep in sync.
 *   - Unfurls. When a teammate pastes an Agora link into a channel, the
 *     preview is authorised as the person who posted it; an unconnected person
 *     gets Slack's private "connect your account" prompt, which lands here.
 *
 * Hidden entirely when the deployment has no Slack app configured — an
 * affordance that cannot complete is worse than no affordance.
 */
export function SlackNotificationSetting() {
  const { t } = useT("settings");
  const qc = useQueryClient();
  const slackEnabled = useConfigStore((s) => s.slackEnabled);
  const beginLink = useBeginSlackUserLink();

  const { data, isLoading } = useQuery({
    queryKey: MY_LINKS_KEY,
    queryFn: () => api.listMyExternalLinks(),
    enabled: slackEnabled,
  });

  if (!slackEnabled) return null;

  const linked = data?.links?.some((l) => l.provider === "slack") === true;

  const connect = async () => {
    try {
      const res = await beginLink.mutateAsync();
      if (!res.authorize_url) {
        toast.error(t(($) => $.notifications.slack.connect_failed));
        return;
      }
      window.open(res.authorize_url, "_blank", "noopener,noreferrer");
      // The callback writes the link on the server; refresh so the row
      // catches up when the person comes back to this tab.
      void qc.invalidateQueries({ queryKey: MY_LINKS_KEY });
    } catch {
      toast.error(t(($) => $.notifications.slack.connect_failed));
    }
  };

  return (
    <section className="space-y-4">
      <div>
        <h2 className="text-sm font-semibold">{t(($) => $.notifications.slack.title)}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(($) => $.notifications.slack.description)}
        </p>
      </div>

      <Card>
        <CardContent className="flex items-start justify-between gap-4">
          <div className="space-y-0.5 pr-4">
            <p className="text-sm font-medium">{t(($) => $.notifications.slack.dm_label)}</p>
            <p className="text-xs text-muted-foreground">
              {isLoading
                ? t(($) => $.notifications.slack.loading)
                : linked
                  ? t(($) => $.notifications.slack.linked_hint)
                  : t(($) => $.notifications.slack.unlinked_hint)}
            </p>
          </div>
          {linked ? (
            <span className="shrink-0 rounded bg-muted px-2 py-1 text-xs text-muted-foreground">
              {t(($) => $.notifications.slack.connected)}
            </span>
          ) : (
            <Button size="sm" disabled={beginLink.isPending || isLoading} onClick={connect}>
              {beginLink.isPending ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
              {t(($) => $.notifications.slack.connect)}
            </Button>
          )}
        </CardContent>
      </Card>
    </section>
  );
}
