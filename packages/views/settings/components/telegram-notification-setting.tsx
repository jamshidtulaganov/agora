"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@agora/core/api";
import { useConfigStore } from "@agora/core/config";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Input } from "@agora/ui/components/ui/input";
import { Label } from "@agora/ui/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { useWorkspacePaths } from "@agora/core/paths";

const MY_LINKS_KEY = ["me", "external-links"] as const;

/**
 * Personal Telegram delivery for inbox events (assign, comment, agent done…).
 * Distinct from Settings → Integrations → Telegram (per-agent bots + groups).
 * Hidden when the platform bot is not configured (no telegram_bot_username).
 */
export function TelegramNotificationSetting() {
  const { t } = useT("settings");
  const qc = useQueryClient();
  const { push } = useNavigation();
  const paths = useWorkspacePaths();
  const botUsername = useConfigStore((s) => s.telegramBotUsername);

  const { data, isLoading } = useQuery({
    queryKey: MY_LINKS_KEY,
    queryFn: () => api.listMyExternalLinks(),
    enabled: !!botUsername,
  });

  const telegramLink = data?.links?.find((l) => l.provider === "telegram") ?? null;

  const [dialogOpen, setDialogOpen] = useState(false);
  const [nonce, setNonce] = useState("");
  const [deepLink, setDeepLink] = useState("");
  const [code, setCode] = useState("");
  const [starting, setStarting] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [unlinking, setUnlinking] = useState(false);

  useEffect(() => {
    if (!dialogOpen) {
      setNonce("");
      setDeepLink("");
      setCode("");
    }
  }, [dialogOpen]);

  if (!botUsername) return null;

  const refresh = () => qc.invalidateQueries({ queryKey: MY_LINKS_KEY });

  const startLink = async () => {
    setStarting(true);
    try {
      const res = await api.startTelegramLink();
      if (!res.nonce || !res.deep_link) {
        toast.error(t(($) => $.notifications.telegram.start_failed));
        return;
      }
      setNonce(res.nonce);
      setDeepLink(res.deep_link);
      setDialogOpen(true);
      window.open(res.deep_link, "_blank", "noopener,noreferrer");
    } catch {
      toast.error(t(($) => $.notifications.telegram.start_failed));
    } finally {
      setStarting(false);
    }
  };

  const verifyLink = async () => {
    if (!nonce || code.trim().length < 6) return;
    setVerifying(true);
    try {
      const link = await api.verifyTelegramLink(nonce, code.trim());
      if (link.provider !== "telegram" || !link.external_id) {
        throw new Error("Malformed Telegram link response");
      }
      await refresh();
      setDialogOpen(false);
      toast.success(t(($) => $.notifications.telegram.toast_linked));
    } catch (err) {
      const message =
        err instanceof ApiError && err.status === 409
          ? t(($) => $.notifications.telegram.toast_conflict)
          : t(($) => $.notifications.telegram.toast_verify_failed);
      toast.error(message);
    } finally {
      setVerifying(false);
    }
  };

  const unlink = async () => {
    setUnlinking(true);
    try {
      await api.unlinkTelegramIdentity();
      await refresh();
      toast.success(t(($) => $.notifications.telegram.toast_unlinked));
    } catch {
      toast.error(t(($) => $.notifications.telegram.toast_unlink_failed));
    } finally {
      setUnlinking(false);
    }
  };

  return (
    <section className="space-y-4">
      <div>
        <h2 className="text-sm font-semibold">{t(($) => $.notifications.telegram.title)}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(($) => $.notifications.telegram.description)}
        </p>
      </div>

      <Card>
        <CardContent className="space-y-4">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-0.5 pr-4">
              <p className="text-sm font-medium">{t(($) => $.notifications.telegram.dm_label)}</p>
              <p className="text-xs text-muted-foreground">
                {isLoading
                  ? t(($) => $.notifications.telegram.loading)
                  : telegramLink
                    ? t(($) => $.notifications.telegram.linked_hint)
                    : t(($) => $.notifications.telegram.unlinked_hint)}
              </p>
            </div>
            {telegramLink ? (
              <Button size="sm" variant="outline" disabled={unlinking} onClick={unlink}>
                {unlinking ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
                {t(($) => $.notifications.telegram.unlink)}
              </Button>
            ) : (
              <Button size="sm" disabled={starting || isLoading} onClick={startLink}>
                {starting ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
                {t(($) => $.notifications.telegram.connect)}
              </Button>
            )}
          </div>

          <div className="border-t pt-4">
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notifications.telegram.groups_hint)}
            </p>
            <Button
              type="button"
              variant="link"
              className="h-auto px-0 pt-1 text-xs"
              onClick={() => push(`${paths.settings()}?tab=integrations`)}
            >
              {t(($) => $.notifications.telegram.groups_link)}
              <ExternalLink className="ml-1 size-3" />
            </Button>
          </div>
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.notifications.telegram.dialog_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.notifications.telegram.dialog_description)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            {deepLink ? (
              <Button
                variant="outline"
                className="w-full"
                onClick={() => window.open(deepLink, "_blank", "noopener,noreferrer")}
              >
                {t(($) => $.notifications.telegram.open_bot)}
                <ExternalLink className="ml-1.5 size-3.5" />
              </Button>
            ) : null}
            <div className="space-y-1.5">
              <Label htmlFor="telegram-link-code">
                {t(($) => $.notifications.telegram.code_label)}
              </Label>
              <Input
                id="telegram-link-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                placeholder="123456"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void verifyLink();
                }}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              {t(($) => $.notifications.telegram.cancel)}
            </Button>
            <Button disabled={verifying || code.trim().length < 6} onClick={verifyLink}>
              {verifying ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
              {t(($) => $.notifications.telegram.verify)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
