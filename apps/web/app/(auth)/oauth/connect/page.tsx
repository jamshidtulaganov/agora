"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuthStore } from "@agora/core/auth";
import { paths } from "@agora/core/paths";
import { ConnectorConsentPage } from "@agora/views/oauth";

// Consent screen for the Claude / ChatGPT MCP connector. Members who are not
// signed in go through login and come back here with the same request.
function ConnectRoute() {
  const router = useRouter();
  const params = useSearchParams();
  const request = params.get("request") ?? "";
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);

  useEffect(() => {
    if (!isLoading && !user) {
      const here = `/oauth/connect?${new URLSearchParams({ request }).toString()}`;
      router.replace(`${paths.login()}?next=${encodeURIComponent(here)}`);
    }
  }, [isLoading, user, router, request]);

  if (isLoading || !user) return null;
  return <ConnectorConsentPage request={request} />;
}

export default function OAuthConnectPage() {
  return (
    <Suspense fallback={null}>
      <ConnectRoute />
    </Suspense>
  );
}
