import { LoginPage } from "@agora/views/auth";
import { DragStrip } from "@agora/views/platform";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";

function requireRuntimeAppUrl(): string {
  const runtimeConfig = window.desktopAPI.runtimeConfig;
  if (!runtimeConfig.ok) {
    throw new Error(
      "Invariant violated: DesktopLoginPage rendered before App accepted runtime config",
    );
  }
  return runtimeConfig.config.appUrl;
}

export function DesktopLoginPage() {
  const webUrl = requireRuntimeAppUrl();
  const handleGoogleLogin = () => {
    // Open web login page in the default browser with platform=desktop flag.
    // The web callback will redirect back via agora:// deep link with the token.
    window.desktopAPI.openExternal(
      `${webUrl}/login?platform=desktop`,
    );
  };

  return (
    <div className="flex h-screen flex-col">
      <DragStrip />
      <LoginPage
        logo={<AgoraIcon bordered size="lg" />}
        onSuccess={() => {
          // Auth store update triggers AppContent re-render → shows DesktopShell.
          // Initial workspace navigation happens in routes.tsx via IndexRedirect.
        }}
        onGoogleLogin={handleGoogleLogin}
      />
    </div>
  );
}
