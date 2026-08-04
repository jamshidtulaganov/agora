"use client";

import { Suspense, useEffect } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { invitationIdFromNextUrl, sanitizeNextUrl } from "@agora/core/auth";
import { paths } from "@agora/core/paths";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { LoginPage } from "@agora/views/auth";
import { useT } from "@agora/views/i18n";
import { setLoggedInCookie } from "@/features/auth/auth-cookie";

function SignupPageContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { t } = useT("auth");
  const nextUrl = sanitizeNextUrl(searchParams.get("next"));
  const invitationId = invitationIdFromNextUrl(nextUrl);
  const invitationSignup =
    nextUrl?.startsWith("/invite/") === true || nextUrl === paths.invitations();
  const loginHref = nextUrl
    ? `${paths.login()}?next=${encodeURIComponent(nextUrl)}`
    : paths.login();
  const { data: invitationAuthInfo, isLoading: invitationAuthLoading } = useQuery({
    queryKey: ["invitation-auth", invitationId],
    queryFn: () => api.getInvitationAuthInfo(invitationId!),
    enabled: invitationId != null,
  });

  useEffect(() => {
    if (invitationId && invitationAuthInfo?.account_exists) {
      router.replace(loginHref);
    }
  }, [invitationAuthInfo?.account_exists, invitationId, loginHref, router]);

  if (invitationId && (invitationAuthLoading || invitationAuthInfo?.account_exists)) {
    return null;
  }

  return (
    <LoginPage
      mode="signup"
      registrationContext={invitationSignup ? "invitation" : "company"}
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
      onTokenObtained={setLoggedInCookie}
      onSuccess={() => router.push(nextUrl ?? paths.onboarding())}
      extra={
        <div className="space-y-3 text-sm text-muted-foreground">
          <p>
            {t(($) => $.signup.have_account)}{" "}
            <Link
              href={loginHref}
              className="font-medium text-foreground underline-offset-4 hover:underline"
            >
              {t(($) => $.signup.sign_in)}
            </Link>
          </p>
          <Link href="/homepage" className="block transition-colors hover:text-foreground">
            ← {t(($) => $.signin.back_home)}
          </Link>
        </div>
      }
    />
  );
}

export default function SignupPage() {
  return (
    <Suspense fallback={null}>
      <SignupPageContent />
    </Suspense>
  );
}
