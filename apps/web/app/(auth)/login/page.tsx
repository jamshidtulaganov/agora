"use client";

import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import {
  invitationIdFromNextUrl,
  sanitizeNextUrl,
  useAuthStore,
} from "@agora/core/auth";
import { workspaceKeys } from "@agora/core/workspace/queries";
import {
  paths,
  resolvePostAuthDestination,
  useHasOnboarded,
} from "@agora/core/paths";
import { api } from "@agora/core/api";
import type { Workspace } from "@agora/core/types";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@agora/ui/components/ui/card";
import { Button } from "@agora/ui/components/ui/button";
import { Loader2 } from "lucide-react";
import { setLoggedInCookie } from "@/features/auth/auth-cookie";
import { LoginPage, validateCliCallback } from "@agora/views/auth";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { useT } from "@agora/views/i18n";

/**
 * Pick where a logged-in user with no explicit `?next=` should land.
 * Un-onboarded users with pending invitations on their email get routed to
 * the batch /invitations page; everyone else falls through to the standard
 * resolver. A network blip on listMyInvitations is non-fatal — we fall
 * through rather than trap the user on an error screen.
 */
async function resolveLoggedInDestination(
  qc: QueryClient,
  hasOnboarded: boolean,
  workspaces: Workspace[],
): Promise<string> {
  if (!hasOnboarded) {
    try {
      const invites = await api.listMyInvitations();
      if (invites.length > 0) {
        qc.setQueryData(workspaceKeys.myInvitations(), invites);
        return paths.invitations();
      }
    } catch {
      // fall through
    }
  }
  return resolvePostAuthDestination(workspaces, hasOnboarded);
}

function LoginPageContent() {
  const router = useRouter();
  const qc = useQueryClient();
  const { t } = useT("auth");
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const searchParams = useSearchParams();

  const cliCallbackRaw = searchParams.get("cli_callback");
  const cliState = searchParams.get("cli_state") || "";
  const platform = searchParams.get("platform");
  const isDesktopHandoff = platform === "desktop" && !cliCallbackRaw;
  // `next` carries a protected URL the user was originally headed to
  // (e.g. /invite/{id}). With URL-driven workspaces there is no legacy
  // "/issues" default — if `next` is absent we decide after login based on
  // the user's workspace list. Sanitize first so a crafted `?next=https://evil`
  // cannot bounce the user off-origin after a successful login.
  const nextUrl = sanitizeNextUrl(searchParams.get("next"));
  const invitationId = invitationIdFromNextUrl(nextUrl);
  const signupHref = nextUrl
    ? `${paths.signup()}?next=${encodeURIComponent(nextUrl)}`
    : paths.signup();
  const { data: invitationAuthInfo, isLoading: invitationAuthLoading } = useQuery({
    queryKey: ["invitation-auth", invitationId],
    queryFn: () => api.getInvitationAuthInfo(invitationId!),
    enabled: invitationId != null,
  });

  const [desktopToken, setDesktopToken] = useState<string | null>(null);
  const [desktopError, setDesktopError] = useState("");
  const hasOnboarded = useHasOnboarded();

  // Middleware sends protected invite URLs through /login. Resolve the
  // bearer invitation here so brand-new invitees land on registration even
  // when they never render the client-side /invite page first.
  useEffect(() => {
    if (invitationId && invitationAuthInfo?.account_exists === false) {
      router.replace(signupHref);
    }
  }, [invitationAuthInfo?.account_exists, invitationId, router, signupHref]);

  // Already authenticated — honor ?next= or fall back to first workspace
  // (or /onboarding if the user has none). Skip this entire path when
  // the user arrived to authorize the CLI.
  useEffect(() => {
    if (isLoading || !user || cliCallbackRaw) return;
    if (isDesktopHandoff) {
      // Desktop opened the browser for login but the web session is already
      // authenticated — mint a bearer token from the cookie session and hand
      // it off via deep link instead of silently redirecting to the workspace.
      api
        .issueCliToken()
        .then(({ token }) => {
          setDesktopToken(token);
          window.location.href = `agora://auth/callback?token=${encodeURIComponent(token)}`;
        })
        .catch((err) => {
          setDesktopError(
            err instanceof Error
              ? err.message
              : t(($) => $.web.desktop_handoff.prepare_failed),
          );
        });
      return;
    }
    if (nextUrl) {
      router.replace(nextUrl);
      return;
    }
    const list = qc.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
    void resolveLoggedInDestination(qc, hasOnboarded, list).then((dest) =>
      router.replace(dest),
    );
  }, [isLoading, user, router, nextUrl, cliCallbackRaw, isDesktopHandoff, hasOnboarded, qc, t]);

  const handleSuccess = async () => {
    // Read the latest user snapshot directly — the closure's `hasOnboarded`
    // was captured before login completed and would be stale here.
    const currentUser = useAuthStore.getState().user;
    const onboarded = currentUser?.onboarded_at != null;
    if (nextUrl) {
      router.push(nextUrl);
      return;
    }
    const list = qc.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
    router.push(await resolveLoggedInDestination(qc, onboarded, list));
  };

  // While the desktop handoff is in progress (or has produced a token/error),
  // render a dedicated screen instead of flashing the login form or redirecting
  // away to a workspace page.
  if (isDesktopHandoff && user) {
    if (desktopError) {
      return (
        <div className="flex min-h-screen items-center justify-center">
          <Card className="w-full max-w-sm">
            <CardHeader className="text-center">
              <CardTitle className="text-2xl">
                {t(($) => $.web.desktop_handoff.failed_title)}
              </CardTitle>
              <CardDescription>{desktopError}</CardDescription>
            </CardHeader>
          </Card>
        </div>
      );
    }
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl">
              {t(($) => $.web.desktop_handoff.opening_title)}
            </CardTitle>
            <CardDescription>
              {desktopToken
                ? t(($) => $.web.desktop_handoff.opening_description)
                : t(($) => $.web.desktop_handoff.preparing)}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            {desktopToken ? (
              <Button
                variant="outline"
                onClick={() => {
                  window.location.href = `agora://auth/callback?token=${encodeURIComponent(desktopToken)}`;
                }}
              >
                {t(($) => $.web.desktop_handoff.open_button)}
              </Button>
            ) : (
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            )}
          </CardContent>
        </Card>
      </div>
    );
  }

  if (
    invitationId &&
    (invitationAuthLoading || invitationAuthInfo?.account_exists === false)
  ) {
    return null;
  }

  return (
    <LoginPage
      initialEmail={invitationAuthInfo?.invitee_email}
      emailLocked={invitationId != null && invitationAuthInfo != null}
      logo={
        <span className="flex items-center gap-2">
          <AgoraIcon className="size-7 text-brand" noSpin />
          <span className="font-serif text-2xl font-medium lowercase tracking-tight">
            agora
          </span>
        </span>
      }
      onSuccess={handleSuccess}
      cliCallback={
        cliCallbackRaw && validateCliCallback(cliCallbackRaw)
          ? { url: cliCallbackRaw, state: cliState }
          : undefined
      }
      onTokenObtained={setLoggedInCookie}
      extra={
        <div className="space-y-3 text-sm text-muted-foreground">
          <p>
            {t(($) => $.signin.no_account)}{" "}
            <Link
              href={signupHref}
              className="font-medium text-foreground underline-offset-4 hover:underline"
            >
              {t(($) => $.signin.create_account)}
            </Link>
          </p>
          <Link
            href="/homepage"
            className="block transition-colors hover:text-foreground"
          >
            ← {t(($) => $.signin.back_home)}
          </Link>
        </div>
      }
    />
  );
}

export default function Page() {
  return (
    <Suspense fallback={null}>
      <LoginPageContent />
    </Suspense>
  );
}
