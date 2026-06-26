import { CoreProvider } from "@agora/core/platform";
import { RESOURCES } from "@agora/views/locales";
import { localStorageAdapter } from "./platform/storage";
import { RouterProvider, type Route } from "./platform/navigation";
import { AuthGate } from "./auth/auth-gate";
import { AppShell } from "./components/app-shell";
import { getStartParam } from "./telegram/sdk";
import { decodeStartParam } from "./telegram/start-param";
import { getLocale } from "./i18n";

const APP_VERSION = "0.1.0";

function deriveWsUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

export function App() {
  // Honors the manual Settings → Language override (then Telegram lang, then ru).
  const locale = getLocale();
  // A deep link may name both the issue and its workspace; switch to that
  // workspace on launch so the (workspace-scoped) issue actually loads.
  const target = decodeStartParam(getStartParam());
  const initial: Route | undefined = target.issueId
    ? { name: "issue", id: target.issueId }
    : undefined;

  return (
    <CoreProvider
      apiBaseUrl=""
      wsUrl={deriveWsUrl()}
      cookieAuth={false}
      storage={localStorageAdapter}
      identity={{ platform: "telegram", version: APP_VERSION }}
      locale={locale}
      resources={{ [locale]: RESOURCES[locale] }}
    >
      <RouterProvider initialRoute={initial}>
        <AuthGate>
          <AppShell deepLinkSlug={target.wsSlug} />
        </AuthGate>
      </RouterProvider>
    </CoreProvider>
  );
}
