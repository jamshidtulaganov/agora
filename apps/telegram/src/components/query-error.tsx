import { WifiOff } from "lucide-react";
import { CenterMessage } from "./center-message";
import { useT } from "../i18n";

// Shared error state for a screen's primary query. Distinct from genuine
// emptiness — a failed fetch must never render "all clear" celebration copy
// (the app is polling-only, so a bad interval otherwise looks like truth).
export function QueryError({ onRetry }: { onRetry: () => void }) {
  const t = useT();
  return (
    <CenterMessage
      icon={<WifiOff className="size-6 text-muted-foreground" />}
      title={t("error.title")}
      subtitle={t("error.sub")}
      actionLabel={t("common.tryAgain")}
      onAction={onRetry}
    />
  );
}
