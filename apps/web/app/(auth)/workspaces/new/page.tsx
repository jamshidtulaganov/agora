"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@agora/core/auth";
import { paths } from "@agora/core/paths";
import {
  myInvitationListOptions,
  workspaceListOptions,
} from "@agora/core/workspace/queries";
import { NewWorkspacePage } from "@agora/views/workspace/new-workspace-page";

export default function Page() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const { data: wsList = [], isFetched: workspacesFetched } = useQuery({
    ...workspaceListOptions(),
    enabled: !!user,
  });
  const hasOnboarded = user?.onboarded_at != null;
  const {
    data: pendingInvitations = [],
    isFetched: invitationsFetched,
  } = useQuery({
    ...myInvitationListOptions(),
    enabled: !!user && !hasOnboarded,
  });

  useEffect(() => {
    if (!isLoading && !user) router.replace(paths.login());
  }, [isLoading, user, router]);

  // `/workspaces/new` is only valid for an already-onboarded user who has no
  // workspace. Recover new invitees from stale/racing redirects: send one
  // pending invitation straight back to its acceptance page, multiple invites
  // to the picker, and a no-invite account to onboarding.
  useEffect(() => {
    if (!user || hasOnboarded || !workspacesFetched || !invitationsFetched) {
      return;
    }
    const onlyInvitation = pendingInvitations[0];
    if (pendingInvitations.length === 1 && onlyInvitation) {
      router.replace(paths.invite(onlyInvitation.id));
    } else if (pendingInvitations.length > 1) {
      router.replace(paths.invitations());
    } else {
      router.replace(paths.onboarding());
    }
  }, [
    hasOnboarded,
    invitationsFetched,
    pendingInvitations,
    router,
    user,
    workspacesFetched,
  ]);

  if (
    isLoading ||
    !user ||
    !workspacesFetched ||
    !hasOnboarded
  ) {
    return null;
  }

  // Always provide an escape. Existing members return to their default
  // workspace; a zero-workspace user returns to the page they came from.
  const onBack = () => {
    if (wsList.length > 0) router.push(paths.root());
    else router.back();
  };

  return (
    <NewWorkspacePage
      onSuccess={(ws) => router.push(paths.workspace(ws.slug).issues())}
      onBack={onBack}
    />
  );
}
