import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { LoginPage } from "@agora/views/auth";
import { useT } from "@agora/views/i18n";
import { DragStrip } from "@agora/views/platform";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { useWindowOverlayStore } from "@/stores/window-overlay-store";

function requireRuntimeApiUrl(): string {
  const runtimeConfig = window.desktopAPI.runtimeConfig;
  if (!runtimeConfig.ok) {
    throw new Error(
      "Invariant violated: DesktopLoginPage rendered before App accepted runtime config",
    );
  }
  return runtimeConfig.config.apiUrl;
}

export function DesktopLoginPage() {
  requireRuntimeApiUrl();
  const { t } = useT("auth");
  const pendingInvitationId = useWindowOverlayStore(
    (state) => state.pendingInvitationId,
  );
  const [standaloneMode, setStandaloneMode] = useState<"login" | "signup">(
    "login",
  );
  const {
    data: invitationAuthInfo,
    isLoading: invitationAuthLoading,
  } = useQuery({
    queryKey: ["invitation-auth", pendingInvitationId],
    queryFn: () => api.getInvitationAuthInfo(pendingInvitationId!),
    enabled: pendingInvitationId != null,
  });

  // A bearer invitation decides the auth mode. Existing users sign in; a
  // brand-new invitee gets the profile form with the target email locked.
  const mode = invitationAuthInfo
    ? invitationAuthInfo.account_exists
      ? "login"
      : "signup"
    : standaloneMode;

  if (pendingInvitationId && invitationAuthLoading) {
    return (
      <div className="flex h-screen flex-col">
        <DragStrip />
        <div className="flex flex-1 items-center justify-center">
          <AgoraIcon className="size-6 animate-pulse" />
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-screen flex-col">
      <DragStrip />
      <LoginPage
        key={`${mode}:${pendingInvitationId ?? "standalone"}`}
        mode={mode}
        registrationContext={pendingInvitationId ? "invitation" : "company"}
        initialEmail={invitationAuthInfo?.invitee_email}
        emailLocked={invitationAuthInfo != null}
        logo={<AgoraIcon bordered size="lg" />}
        onSuccess={() => {
          // Auth store update triggers AppContent re-render → shows DesktopShell.
          // Initial workspace navigation happens in routes.tsx via IndexRedirect.
        }}
        extra={
          pendingInvitationId ? undefined : (
            <p className="text-sm text-muted-foreground">
              {mode === "login"
                ? t(($) => $.signin.no_account)
                : t(($) => $.signup.have_account)}{" "}
              <button
                type="button"
                className="font-medium text-foreground underline-offset-4 hover:underline"
                onClick={() =>
                  setStandaloneMode(mode === "login" ? "signup" : "login")
                }
              >
                {mode === "login"
                  ? t(($) => $.signin.create_account)
                  : t(($) => $.signup.sign_in)}
              </button>
            </p>
          )
        }
      />
    </div>
  );
}
