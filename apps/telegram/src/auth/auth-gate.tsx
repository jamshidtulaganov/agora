import type { ReactNode } from "react";
import { useTelegramAuth } from "./use-telegram-auth";
import { CenterMessage } from "../components/center-message";

// Blocks the app until the Mini App has a session. Loading → spinner;
// opened-outside-Telegram or auth failure → message (with retry on failure).
export function AuthGate({ children }: { children: ReactNode }) {
  const { status, reason, retry } = useTelegramAuth();

  if (status === "authed") return <>{children}</>;

  if (status === "error") {
    if (reason === "not-telegram") {
      return (
        <CenterMessage
          title="Open in Telegram"
          subtitle="This app signs you in automatically when opened from the Agora bot."
        />
      );
    }
    return (
      <CenterMessage
        title="Couldn’t sign you in"
        subtitle="Your Telegram session may have expired. Try again."
        actionLabel="Try again"
        onAction={retry}
      />
    );
  }

  return <CenterMessage spinner title="Signing in…" />;
}
