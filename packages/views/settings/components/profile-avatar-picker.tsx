"use client";

import { useRef } from "react";
import { Camera, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { useAuthStore } from "@agora/core/auth";
import { api } from "@agora/core/api";
import { resolvePublicFileUrl } from "@agora/core/workspace/avatar-url";
import { useFileUpload } from "@agora/core/hooks/use-file-upload";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

/**
 * The current user's photo as a button: click to pick an image, which is
 * uploaded and saved to the profile straight away. Shared by Settings →
 * Profile and the first-login member setup.
 */
export function ProfileAvatarPicker({ size = "md" }: { size?: "md" | "lg" }) {
  const { t } = useT("settings");
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  const { upload, uploading } = useFileUpload(api);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const initials = (user?.name ?? "")
    .split(" ")
    .map((w) => w[0])
    .join("")
    .toUpperCase()
    .slice(0, 2);

  const handleAvatarUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    // Reset input so the same file can be re-selected
    e.target.value = "";
    try {
      const result = await upload(file);
      if (!result) return;
      const updated = await api.updateMe({ avatar_url: result.link });
      setUser(updated);
      toast.success(t(($) => $.account.toast_avatar_updated));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t(($) => $.account.toast_avatar_failed));
    }
  };

  return (
    <>
      <button
        type="button"
        aria-label={t(($) => $.account.click_avatar_hint)}
        className={cn(
          "group relative shrink-0 overflow-hidden rounded-full bg-muted focus:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          size === "lg" ? "h-24 w-24" : "h-16 w-16",
        )}
        onClick={() => fileInputRef.current?.click()}
        disabled={uploading}
      >
        {user?.avatar_url ? (
          <img
            src={resolvePublicFileUrl(user.avatar_url) ?? undefined}
            alt={user.name}
            className="h-full w-full object-cover"
          />
        ) : (
          <span
            className={cn(
              "flex h-full w-full items-center justify-center font-semibold text-muted-foreground",
              size === "lg" ? "text-2xl" : "text-lg",
            )}
          >
            {initials}
          </span>
        )}
        <div className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">
          {uploading ? (
            <Loader2 className="h-5 w-5 animate-spin text-white" />
          ) : (
            <Camera className="h-5 w-5 text-white" />
          )}
        </div>
      </button>
      <input ref={fileInputRef} type="file" accept="image/*" className="hidden" onChange={handleAvatarUpload} />
    </>
  );
}
