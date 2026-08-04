"use client";

import { useEffect } from "react";
import { useRouter, useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { useAuthStore } from "@agora/core/auth";
import { paths, resolveInvitationAuthDestination } from "@agora/core/paths";
import { workspaceListOptions } from "@agora/core/workspace/queries";
import { InvitePage } from "@agora/views/invite";

export default function InviteAcceptPage() {
  const router = useRouter();
  const params = useParams<{ id: string }>();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const { data: invitationAuthInfo, isError: invitationAuthError } = useQuery({
    queryKey: ["invitation-auth", params.id],
    queryFn: () => api.getInvitationAuthInfo(params.id),
    enabled: !isLoading && !user,
  });
  const { data: wsList = [] } = useQuery({
    ...workspaceListOptions(),
    enabled: !!user,
  });

  // Existing invitees sign in; brand-new invitees land directly on the
  // registration form with the invitation email prefilled and locked.
  useEffect(() => {
    if (isLoading || user) return;
    if (invitationAuthInfo) {
      router.replace(
        resolveInvitationAuthDestination(
          params.id,
          invitationAuthInfo.account_exists,
        ),
      );
    } else if (invitationAuthError) {
      router.replace(resolveInvitationAuthDestination(params.id, true));
    }
  }, [invitationAuthError, invitationAuthInfo, isLoading, params.id, router, user]);

  if (isLoading || !user) return null;

  const onBack =
    wsList.length > 0 ? () => router.push(paths.root()) : undefined;

  return <InvitePage invitationId={params.id} onBack={onBack} />;
}
