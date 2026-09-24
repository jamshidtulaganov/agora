"use client";

import { useEffect, useState } from "react";
import { Save, Trash2 } from "lucide-react";
import { Input } from "@agora/ui/components/ui/input";
import { Label } from "@agora/ui/components/ui/label";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { toast } from "sonner";
import { useAuthStore } from "@agora/core/auth";
import { api, ApiError } from "@agora/core/api";
import { useT } from "../../i18n";
import { DeleteAccountDialog } from "./delete-account-dialog";
import { ProfileAvatarPicker } from "./profile-avatar-picker";
import { ZohoAccountCard } from "./zoho-account-card";

// Mirror server/internal/handler/auth.go:MaxProfileDescriptionLen. Counted in
// JS String.length (UTF-16 code units) here while the server counts runes,
// so a profile full of supplementary-plane emoji will trip the client cap
// before the server's — which is the safer direction of drift.
const MAX_PROFILE_DESCRIPTION_LEN = 2000;

export function AccountTab() {
  const { t } = useT("settings");
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  const logout = useAuthStore((s) => s.logout);

  const [profileName, setProfileName] = useState(user?.name ?? "");
  const [profileDescription, setProfileDescription] = useState(
    user?.profile_description ?? "",
  );
  const [profileSaving, setProfileSaving] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deletePending, setDeletePending] = useState(false);

  useEffect(() => {
    setProfileName(user?.name ?? "");
    setProfileDescription(user?.profile_description ?? "");
  }, [user]);

  const descriptionTooLong = profileDescription.length > MAX_PROFILE_DESCRIPTION_LEN;

  const handleProfileSave = async () => {
    if (descriptionTooLong) return;
    setProfileSaving(true);
    try {
      const updated = await api.updateMe({
        name: profileName,
        profile_description: profileDescription,
      });
      setUser(updated);
      toast.success(t(($) => $.account.toast_profile_updated));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.account.toast_profile_failed));
    } finally {
      setProfileSaving(false);
    }
  };

  const handleDeleteAccount = async () => {
    if (!user) return;
    setDeletePending(true);
    try {
      await api.deleteMe(user.email);
      logout();
    } catch (error) {
      if (error instanceof ApiError) {
        const body = error.body as {
          code?: string;
          workspaces?: Array<{ name?: string }>;
        } | undefined;
        if (body?.code === "account_owns_workspaces") {
          const names = body.workspaces
            ?.map((workspace) => workspace.name)
            .filter((name): name is string => Boolean(name))
            .join(", ");
          toast.error(
            t(($) => $.account.delete_blocked, {
              workspaces: names || t(($) => $.account.delete_blocked_fallback),
            }),
          );
          return;
        }
      }
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.account.toast_delete_failed),
      );
    } finally {
      setDeletePending(false);
    }
  };

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <h2 className="text-sm font-semibold">{t(($) => $.account.section_profile)}</h2>

        <Card>
          <CardContent className="space-y-4">
            {/* Avatar upload */}
            <div className="flex items-center gap-4">
              <ProfileAvatarPicker />
              <div className="text-xs text-muted-foreground">
                {t(($) => $.account.click_avatar_hint)}
              </div>
            </div>

            <div>
              <Label className="text-xs text-muted-foreground">{t(($) => $.account.name_label)}</Label>
              <Input
                type="search"
                value={profileName}
                onChange={(e) => setProfileName(e.target.value)}
                className="mt-1"
              />
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">
                {t(($) => $.account.profile_description_label)}
              </Label>
              <Textarea
                value={profileDescription}
                onChange={(e) => setProfileDescription(e.target.value)}
                placeholder={t(($) => $.account.profile_description_placeholder)}
                rows={5}
                maxLength={MAX_PROFILE_DESCRIPTION_LEN}
                className="mt-1 resize-y"
              />
              <div className="mt-1 flex items-start justify-between gap-3 text-xs text-muted-foreground">
                <span>{t(($) => $.account.profile_description_hint)}</span>
                <span
                  className={descriptionTooLong ? "text-destructive shrink-0" : "shrink-0"}
                  aria-live="polite"
                >
                  {profileDescription.length}/{MAX_PROFILE_DESCRIPTION_LEN}
                </span>
              </div>
              {descriptionTooLong ? (
                <p className="mt-1 text-xs text-destructive">
                  {t(($) => $.account.profile_description_too_long, {
                    max: MAX_PROFILE_DESCRIPTION_LEN,
                    count: profileDescription.length,
                  })}
                </p>
              ) : null}
            </div>
            <div className="flex items-center justify-end gap-2 pt-1">
              <Button
                size="sm"
                onClick={handleProfileSave}
                disabled={profileSaving || !profileName.trim() || descriptionTooLong}
              >
                <Save className="h-3 w-3" />
                {profileSaving ? t(($) => $.account.saving) : t(($) => $.account.save)}
              </Button>
            </div>
          </CardContent>
        </Card>
      </section>

      <section className="space-y-4">
        <h2 className="text-sm font-semibold">
          {t(($) => $.account.section_connected_accounts)}
        </h2>
        <ZohoAccountCard />
      </section>

      <section className="space-y-4">
        <h2 className="text-sm font-semibold text-destructive">
          {t(($) => $.account.danger_zone)}
        </h2>
        <Card className="border-destructive/40">
          <CardContent className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="space-y-1">
              <h3 className="text-sm font-medium">
                {t(($) => $.account.delete_title)}
              </h3>
              <p className="max-w-2xl text-xs text-muted-foreground">
                {t(($) => $.account.delete_description)}
              </p>
            </div>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              className="shrink-0"
              onClick={() => setDeleteOpen(true)}
              disabled={!user}
            >
              <Trash2 className="h-3.5 w-3.5" />
              {t(($) => $.account.delete_button)}
            </Button>
          </CardContent>
        </Card>
      </section>

      <DeleteAccountDialog
        email={user?.email ?? ""}
        loading={deletePending}
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        onConfirm={handleDeleteAccount}
      />
    </div>
  );
}
