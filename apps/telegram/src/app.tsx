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

function initialRoute(): Route | undefined {
  const issueId = decodeStartParam(getStartParam());
  return issueId ? { name: "issue", id: issueId } : undefined;
}

export function App() {
  const locale = resolveLocale();

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
      <RouterProvider initialRoute={initialRoute()}>
        <AuthGate>
          <AppShell />
        </AuthGate>
      </RouterProvider>
    </CoreProvider>
  );
}
