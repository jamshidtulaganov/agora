"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuthStore } from "@agora/core/auth";
import { paths } from "@agora/core/paths";
import { AdminPage } from "@agora/views/admin";

// Platform admin dashboard. Global (not workspace-scoped) and web-only. The
// server decides who may see it (AGORA_PLATFORM_ADMIN_EMAILS); this page only
// makes sure someone is signed in.
export default function Page() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);

  useEffect(() => {
    if (!isLoading && !user) router.replace(paths.login());
  }, [isLoading, user, router]);

  if (isLoading || !user) return null;
  return <AdminPage />;
}
