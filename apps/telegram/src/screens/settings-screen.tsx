import { useState } from "react";
import { ChevronLeft, ChevronRight, Check } from "lucide-react";
import { notificationPreferenceOptions } from "@agora/core/notification-preferences/queries";
import { useUpdateNotificationPreferences } from "@agora/core/notification-preferences/mutations";
import { useWorkspaceId } from "@agora/core/hooks";
import { useQuery } from "@tanstack/react-query";
import type { NotificationGroupKey, NotificationPreferences } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { BottomSheet } from "../components/bottom-sheet";
import { haptic } from "../telegram/sdk";
import { getLocale, setLocaleOverride, LOCALES, LOCALE_NAMES, useT } from "../i18n";
import { cn } from "../lib/cn";

const APP_VERSION = "0.1.0";

const GROUPS: NotificationGroupKey[] = [
  "assignments",
  "status_changes",
  "comments",
  "updates",
  "agent_activity",
  "system_notifications",
];

export function SettingsScreen() {
  const wsId = useWorkspaceId();
  const { back } = useRouter();
  const t = useT();
  const { data } = useQuery(notificationPreferenceOptions(wsId));
  const update = useUpdateNotificationPreferences();
  const prefs: NotificationPreferences = data?.preferences ?? {};
  const [langOpen, setLangOpen] = useState(false);
  const locale = getLocale();

  const isOn = (g: NotificationGroupKey) => prefs[g] !== "muted";
  const toggle = (g: NotificationGroupKey) => {
    haptic("light");
    update.mutate({ ...prefs, [g]: isOn(g) ? "muted" : "all" });
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center gap-1 border-b border-border bg-card px-2 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
        <button type="button" onClick={back} className="px-1 py-1 text-muted-foreground">
          <ChevronLeft className="size-5" />
        </button>
        <span className="text-sm font-semibold text-foreground">{t("settings.title")}</span>
      </header>

      <div className="flex-1 overflow-y-auto">
        <SectionTitle>{t("settings.notifications")}</SectionTitle>
        <div className="divide-y divide-border border-y border-border">
          {GROUPS.map((g) => (
            <div key={g} className="flex items-center justify-between gap-3 bg-card px-4 py-3">
              <span className="text-sm text-foreground">{t(`notif.${g}`)}</span>
              <Toggle checked={isOn(g)} onChange={() => toggle(g)} />
            </div>
          ))}
        </div>
        <p className="px-4 py-2 text-xs text-muted-foreground">{t("settings.notifHint")}</p>

        <SectionTitle>{t("settings.about")}</SectionTitle>
        <div className="divide-y divide-border border-y border-border">
          <button
            type="button"
            onClick={() => setLangOpen(true)}
            className="flex w-full items-center justify-between gap-3 bg-card px-4 py-3 text-left transition-colors active:bg-accent"
          >
            <span className="text-sm text-muted-foreground">{t("settings.language")}</span>
            <span className="flex items-center gap-1 text-sm font-medium text-foreground">
              {LOCALE_NAMES[locale]}
              <ChevronRight className="size-4 text-muted-foreground/60" />
            </span>
          </button>
          <InfoRow label={t("settings.version")} value={APP_VERSION} />
        </div>
      </div>

      <BottomSheet open={langOpen} onClose={() => setLangOpen(false)} title={t("settings.language")}>
        <ul className="pb-2">
          {LOCALES.map((l) => (
            <li key={l}>
              <button
                type="button"
                onClick={() => {
                  if (l === locale) {
                    setLangOpen(false);
                    return;
                  }
                  haptic("light");
                  setLocaleOverride(l); // persists + reloads
                }}
                className="flex w-full items-center gap-2.5 px-4 py-3 text-left text-sm transition-colors active:bg-accent"
              >
                <span className="flex-1">{LOCALE_NAMES[l]}</span>
                {l === locale && <Check className="size-4 text-brand" />}
              </button>
            </li>
          ))}
        </ul>
      </BottomSheet>
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <div className="px-4 pb-1.5 pt-4 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
      {children}
    </div>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3 bg-card px-4 py-3">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="text-sm font-medium text-foreground">{value}</span>
    </div>
  );
}

// A clean iOS-style switch: the knob sits inside a padded track, so it never
// overflows on the right (the previous absolute-positioned version did).
function Toggle({ checked, onChange }: { checked: boolean; onChange: () => void }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={onChange}
      className={cn(
        "flex h-6 w-11 shrink-0 items-center rounded-full p-0.5 transition-colors",
        checked ? "bg-brand" : "bg-muted",
      )}
    >
      <span
        className={cn(
          "size-5 rounded-full bg-white shadow-sm transition-transform",
          checked ? "translate-x-5" : "translate-x-0",
        )}
      />
    </button>
  );
}
