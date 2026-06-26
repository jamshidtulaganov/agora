import { CoreProvider } from "@agora/core/platform";
import { RESOURCES } from "@agora/views/locales";
import type { SupportedLocale } from "@agora/core/i18n";
import { localStorageAdapter } from "./platform/storage";
import { RouterProvider, type Route } from "./platform/navigation";
import { AuthGate } from "./auth/auth-gate";
import { AppShell } from "./components/app-shell";
import { getLanguageCode, getStartParam } from "./telegram/sdk";
import { decodeStartParam } from "./telegram/start-param";

const APP_VERSION = "0.1.0";

function resolveLocale(): SupportedLocale {
  const code = getLanguageCode().toLowerCase();
  if (code.startsWith("ru")) return "ru";
  if (code.startsWith("uz")) return "uz";
  if (code.startsWith("zh")) return "zh-Hans";
  if (code.startsWith("en")) return "en";
  return "ru"; // SalesDoctor default audience
}

function deriveWsUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

export function App() {
  const locale = resolveLocale();
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
