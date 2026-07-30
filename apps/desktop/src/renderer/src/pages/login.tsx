import { LoginPage } from "@agora/views/auth";
import { DragStrip } from "@agora/views/platform";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";

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
  const apiUrl = requireRuntimeApiUrl();
  const handleGoogleLogin = () => {
    // Backend-only deployments do not have a Next.js /login page. Start OAuth
    // on the API origin; its callback returns the token through agora://.
    window.desktopAPI.openExternal(
      `${apiUrl}/auth/google/desktop/start`,
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
