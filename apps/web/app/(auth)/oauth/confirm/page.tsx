"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuthStore } from "@agora/core/auth";
import { paths } from "@agora/core/paths";
import { ConfirmActionPage } from "@agora/views/oauth";

// Where a member confirms a destructive action Claude or ChatGPT asked for
// through the MCP connector. Signed-out members go through login first.
function ConfirmRoute() {
  const router = useRouter();
  const params = useSearchParams();
  const op = params.get("op") ?? "";
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);

  useEffect(() => {
    if (!isLoading && !user) {
      const here = `/oauth/confirm?${new URLSearchParams({ op }).toString()}`;
      router.replace(`${paths.login()}?next=${encodeURIComponent(here)}`);
    }
  }, [isLoading, user, router, op]);

  if (isLoading || !user) return null;
  return <ConfirmActionPage operationId={op} />;
}

export default function OAuthConfirmPage() {
  return (
    <Suspense fallback={null}>
      <ConfirmRoute />
    </Suspense>
  );
}
