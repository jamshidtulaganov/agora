"use client";

import { useEffect, useRef, useState } from "react";
import { Check, CircleUser, Inbox, ListTodo, Sparkles } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useAuthStore } from "@agora/core/auth";
import { WorkspaceSlugProvider } from "@agora/core/paths";
import type { Workspace } from "@agora/core/types";
import { Input } from "@agora/ui/components/ui/input";
import { Label } from "@agora/ui/components/ui/label";
import { Switch } from "@agora/ui/components/ui/switch";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { ProfileAvatarPicker } from "../../settings/components/profile-avatar-picker";
import { BrowserNotificationSetting } from "../../settings/components/browser-notification-setting";
import { TelegramNotificationSetting } from "../../settings/components/telegram-notification-setting";

function StepTitle({ title, lede }: { title: string; lede: string }) {
  return (
    <div className="mb-8 flex flex-col gap-3">
      <h1 className="text-balance font-serif text-4xl font-medium leading-[1.08] tracking-tight sm:text-5xl">
        {title}
      </h1>
      <p className="text-base leading-relaxed text-muted-foreground">{lede}</p>
    </div>
  );
}

// --- 1. What Agora is -------------------------------------------------------

// Keyed by sidebar nav label, so each row is named exactly as the item the
// person will look for in the sidebar.
const FEATURES: { key: "issues" | "my_issues" | "inbox" | "assistant"; icon: LucideIcon }[] = [
  { key: "issues", icon: ListTodo },
  { key: "my_issues", icon: CircleUser },
  { key: "inbox", icon: Inbox },
  { key: "assistant", icon: Sparkles },
];

export function StepAbout({ workspaces }: { workspaces: Workspace[] }) {
  const { t } = useT("onboarding");
  const { t: tNav } = useT("layout");
  return (
    <>
      <StepTitle
        title={t(($) => $.member_setup.about.title)}
        lede={t(($) => $.member_setup.about.lede, { count: workspaces.length })}
      />
      <ul className="flex flex-col divide-y rounded-xl border bg-card">
        {FEATURES.map(({ key, icon: Icon }) => (
          <li key={key} className="flex gap-4 px-4 py-4">
            <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg bg-brand/10 text-brand">
              <Icon className="size-4" />
            </span>
            <div className="min-w-0">
              <p className="text-sm font-medium">{tNav(($) => $.nav[key])}</p>
              <p className="mt-0.5 text-sm leading-relaxed text-muted-foreground">
                {t(($) => $.member_setup.about.features[key].body)}
              </p>
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}

// --- 2. Name + photo ---------------------------------------------------------

export function StepProfile({
  registerSave,
}: {
  /** Hands the flow a save to run on Continue; it resolves false to stay. */
  registerSave: (save: (() => Promise<boolean>) | null) => void;
}) {
  const { t } = useT("onboarding");
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  const [name, setName] = useState(user?.name ?? "");
  const nameRef = useRef(name);
  nameRef.current = name;

  useEffect(() => {
    registerSave(async () => {
      const trimmed = nameRef.current.trim();
      if (!trimmed) {
        toast.error(t(($) => $.member_setup.profile.name_required));
        return false;
      }
      if (trimmed === (useAuthStore.getState().user?.name ?? "")) return true;
      try {
        setUser(await api.updateMe({ name: trimmed }));
        return true;
      } catch (err) {
        toast.error(err instanceof Error ? err.message : t(($) => $.member_setup.profile.save_failed));
        return false;
      }
    });
    return () => registerSave(null);
    // registerSave/setUser are stable; the save reads the name through a ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <>
      <StepTitle
        title={t(($) => $.member_setup.profile.title)}
        lede={t(($) => $.member_setup.profile.lede)}
      />
      <div className="flex flex-col gap-6 rounded-xl border bg-card p-5">
        <div className="flex items-center gap-4">
          <ProfileAvatarPicker size="lg" />
          <p className="text-sm text-muted-foreground">{t(($) => $.member_setup.profile.photo_hint)}</p>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="member-setup-name">{t(($) => $.member_setup.profile.name_label)}</Label>
          <Input
            id="member-setup-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoComplete="name"
            maxLength={120}
          />
          <p className="text-xs text-muted-foreground">{t(($) => $.member_setup.profile.name_hint)}</p>
        </div>
      </div>
    </>
  );
}

// --- 3. Notifications --------------------------------------------------------

export function StepNotifications({ workspaceSlug }: { workspaceSlug: string | null }) {
  const { t } = useT("onboarding");
  return (
    <>
      <StepTitle
        title={t(($) => $.member_setup.notifications.title)}
        lede={t(($) => $.member_setup.notifications.lede)}
      />
      {/* The Telegram setting links to workspace settings, so it needs a
          workspace context; onboarding has none, so lend it the first one. */}
      <WorkspaceSlugProvider slug={workspaceSlug}>
        <div className="flex flex-col gap-3">
          <BrowserNotificationSetting />
          {workspaceSlug && <TelegramNotificationSetting />}
        </div>
      </WorkspaceSlugProvider>
      <p className="mt-4 text-xs text-muted-foreground">{t(($) => $.member_setup.notifications.later)}</p>
    </>
  );
}

// --- 4. Your workspaces -------------------------------------------------------

export function StepWorkspaces({
  workspaces,
  selectedId,
  onSelect,
  withTour,
  onWithTourChange,
}: {
  workspaces: Workspace[];
  selectedId: string | null;
  onSelect: (workspace: Workspace) => void;
  withTour: boolean;
  onWithTourChange: (value: boolean) => void;
}) {
  const { t } = useT("onboarding");
  return (
    <>
      <StepTitle
        title={t(($) => $.member_setup.workspaces.title)}
        lede={t(($) => $.member_setup.workspaces.lede, { count: workspaces.length })}
      />
      <div
        role="radiogroup"
        aria-label={t(($) => $.member_setup.workspaces.title)}
        className="flex max-h-[340px] flex-col gap-1.5 overflow-y-auto pr-1"
      >
        {workspaces.map((workspace) => {
          const selected = workspace.id === selectedId;
          return (
            <button
              key={workspace.id}
              type="button"
              role="radio"
              aria-checked={selected}
              onClick={() => onSelect(workspace)}
              className={cn(
                "flex items-center gap-3 rounded-lg border bg-card px-3 py-2.5 text-left transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                selected && "border-brand bg-brand/5 hover:bg-brand/5",
              )}
            >
              <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-sm font-semibold text-muted-foreground">
                {workspace.name.trim().charAt(0).toUpperCase() || "·"}
              </span>
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{workspace.name}</span>
              {selected && <Check className="size-4 shrink-0 text-brand" />}
            </button>
          );
        })}
      </div>
      <label className="mt-6 flex items-center justify-between gap-4 rounded-lg border bg-card px-4 py-3">
        <span className="flex flex-col gap-0.5">
          <span className="text-sm font-medium">{t(($) => $.member_setup.workspaces.tour_label)}</span>
          <span className="text-xs text-muted-foreground">{t(($) => $.member_setup.workspaces.tour_hint)}</span>
        </span>
        <Switch checked={withTour} onCheckedChange={(value) => onWithTourChange(value === true)} />
      </label>
    </>
  );
}
