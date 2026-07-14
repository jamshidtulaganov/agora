import { CircleCheck } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { useCurrentMember } from "@agora/core/permissions";
import { notificationPreferenceOptions } from "@agora/core/notification-preferences/queries";
import { useUpdateNotificationPreferences } from "@agora/core/notification-preferences/mutations";
import type {
  NotificationGroupKey,
  NotificationPreferences,
  Workspace,
} from "@agora/core/types";
import { Avatar } from "../components/avatar";
import { haptic } from "../telegram/sdk";
import { getLocale, setLocaleOverride, LOCALE_NAMES, useT, type Locale } from "../i18n";
import { cn } from "../lib/cn";

// Profile tab (design 5a §2.6): identity card, language picker card, and the
// notification toggles + version line absorbed from the old settings screen.
// All data is store/cache-backed, so the screen renders immediately — no
// skeleton state.

// Also injected as the client identity version in app.tsx — keep in sync
// until it moves to a single shared constant.
const APP_VERSION = "0.1.0";

const CARD =
  "rounded-xl border border-border bg-card shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none";

// Design order: English → Oʻzbekcha → Русский (differs from LOCALES order).
const LANGUAGE_ROWS: Locale[] = ["en", "uz", "ru"];

const NOTIFICATION_GROUPS: NotificationGroupKey[] = [
  "assignments",
  "status_changes",
  "comments",
  "updates",
  "agent_activity",
  "system_notifications",
];

// Known workspace roles → i18n keys. Unknown server values downgrade to the
// generic member label instead of crashing (enum-drift rule).
const ROLE_KEYS: Record<string, string> = {
  owner: "role.owner",
  admin: "role.admin",
  member: "role.member",
};

export function ProfileScreen({ workspace }: { workspace: Workspace }) {
  const wsId = useWorkspaceId();
  const t = useT();
  const user = useAuthStore((s) => s.user);
  const { role } = useCurrentMember(wsId);
  const locale = getLocale();

  const { data } = useQuery(notificationPreferenceOptions(wsId));
  const update = useUpdateNotificationPreferences();
  const prefs: NotificationPreferences = data?.preferences ?? {};

  const isOn = (g: NotificationGroupKey) => prefs[g] !== "muted";
  const toggle = (g: NotificationGroupKey) => {
    haptic("light");
    update.mutate({ ...prefs, [g]: isOn(g) ? "muted" : "all" });
  };

  const pickLanguage = (l: Locale) => {
    if (l === locale) return;
    haptic("light");
    setLocaleOverride(l); // persists the override and reloads the app
  };

  const name = user?.name ?? "";
  const roleKey = role !== null ? (ROLE_KEYS[role] ?? "role.member") : null;

  return (
    <div className="flex min-h-0 flex-1 animate-ag-fade-in flex-col gap-2.5 overflow-y-auto px-4 pb-6 pt-2.5">
      {/* Title */}
      <h1 className="px-1 pb-0.5 text-[26px] font-bold tracking-[-0.4px] text-foreground">
        {t("profile.title")}
      </h1>

      {/* Identity card */}
      <section className={cn(CARD, "flex items-center gap-3.5 px-4 py-[18px]")}>
        <Avatar name={name || "?"} avatarUrl={user?.avatar_url} size={54} className="shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-[17px] font-semibold tracking-[-0.2px] text-foreground">
            {name}
          </div>
          <div className="mt-0.5 truncate text-[13px] text-muted-foreground">
            {user?.email ?? ""} · {t("profile.workspaceOf", { name: workspace.name })}
          </div>
        </div>
        {roleKey !== null && (
          <span className="shrink-0 rounded-full bg-brand/10 px-2.5 py-1 text-[11px] font-semibold text-brand">
            {t(roleKey)}
          </span>
        )}
      </section>

      {/* Language card */}
      <section className={cn(CARD, "divide-y divide-border/60 overflow-hidden")}>
        {LANGUAGE_ROWS.map((l) => {
          const selected = l === locale;
          return (
            <button
              key={l}
              type="button"
              onClick={() => pickLanguage(l)}
              aria-pressed={selected}
              className="flex w-full items-center gap-3 px-4 py-3.5 text-left transition-colors active:bg-accent"
            >
              <span className="flex-1 truncate text-[14.5px] font-medium text-foreground">
                {LOCALE_NAMES[l]}
              </span>
              <span className="text-xs text-muted-foreground">{l.toUpperCase()}</span>
              {selected ? (
                <CircleCheck className="size-[22px] shrink-0 text-brand" />
              ) : (
                <span className="size-[18px] shrink-0 rounded-full border-[1.6px] border-border" />
              )}
            </button>
          );
        })}
      </section>

      {/* Notifications */}
      <div className="mt-1.5 px-1.5 text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
        {t("settings.notifications")}
      </div>
      <section className={cn(CARD, "divide-y divide-border/60 overflow-hidden")}>
        {NOTIFICATION_GROUPS.map((g) => (
          <div key={g} className="flex items-center justify-between gap-3 px-4 py-3">
            <span className="text-sm text-foreground">{t(`notif.${g}`)}</span>
            <Toggle checked={isOn(g)} onChange={() => toggle(g)} />
          </div>
        ))}
      </section>
      <p className="px-1.5 text-xs text-muted-foreground">{t("settings.notifHint")}</p>

      {/* Footer */}
      <p className="pb-1 pt-2 text-center text-[11px] text-muted-foreground/70">
        Agora mini app · v{APP_VERSION}
      </p>
    </div>
  );
}

// iOS-style switch ported from the old settings screen: the knob sits inside
// a padded track, so it never overflows on the right.
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
