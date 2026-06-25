import type { ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { Button } from "@agora/ui/components/ui/button";
import { useTelegramAuth } from "./use-telegram-auth";
import { useT } from "../i18n";

// Blocks the app until the Mini App has a session. Every state is a branded
// splash (Agora assembly mark + message), so the logo is the first thing users
// see while it auto-signs them in.
export function AuthGate({ children }: { children: ReactNode }) {
  const { status, reason, retry } = useTelegramAuth();
  const t = useT();

  if (status === "authed") return <>{children}</>;

  if (status === "error") {
    if (reason === "not-telegram") {
      return (
        <BrandSplash title={t("auth.openInTelegram")} subtitle={t("auth.openInTelegramSub")} />
      );
    }
    return (
      <BrandSplash
        title={t("auth.failed")}
        subtitle={t("auth.failedSub")}
        actionLabel={t("common.tryAgain")}
        onAction={retry}
      />
    );
  }

  return <BrandSplash title={t("auth.signingIn")} loading />;
}

function BrandSplash({
  title,
  subtitle,
  loading,
  actionLabel,
  onAction,
}: {
  title: string;
  subtitle?: string;
  loading?: boolean;
  actionLabel?: string;
  onAction?: () => void;
}) {
  return (
    <div className="flex h-full flex-1 flex-col items-center justify-center gap-4 px-8 text-center">
      <AgoraIcon className="size-16 text-foreground" animate noSpin />
      <div className="text-lg font-semibold tracking-tight text-foreground">Agora</div>
      <div className="text-sm text-muted-foreground">{title}</div>
      {subtitle && <div className="text-xs text-muted-foreground/80">{subtitle}</div>}
      {loading && <Loader2 className="size-5 animate-spin text-muted-foreground" />}
      {actionLabel && onAction && (
        <Button variant="outline" size="sm" onClick={onAction} className="mt-1">
          {actionLabel}
        </Button>
      )}
    </div>
  );
}
