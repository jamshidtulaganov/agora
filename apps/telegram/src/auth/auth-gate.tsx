import type { ReactNode } from "react";
import { useTelegramAuth } from "./use-telegram-auth";
import { CenterMessage } from "../components/center-message";
import { useT } from "../i18n";

// Blocks the app until the Mini App has a session. Loading → spinner;
// opened-outside-Telegram or auth failure → message (with retry on failure).
export function AuthGate({ children }: { children: ReactNode }) {
  const { status, reason, retry } = useTelegramAuth();
  const t = useT();

  if (status === "authed") return <>{children}</>;

  if (status === "error") {
    if (reason === "not-telegram") {
      return (
        <CenterMessage
          title={t("auth.openInTelegram")}
          subtitle={t("auth.openInTelegramSub")}
        />
      );
    }
    return (
      <CenterMessage
        title={t("auth.failed")}
        subtitle={t("auth.failedSub")}
        actionLabel={t("common.tryAgain")}
        onAction={retry}
      />
    );
  }

  return <CenterMessage spinner title={t("auth.signingIn")} />;
}
