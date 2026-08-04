"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@agora/ui/components/ui/card";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSlot,
} from "@agora/ui/components/ui/input-otp";
import { Label } from "@agora/ui/components/ui/label";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { useAuthStore } from "@agora/core/auth";
import { api } from "@agora/core/api";
import type { User, Workspace } from "@agora/core/types";
import { workspaceKeys } from "@agora/core/workspace/queries";
import { useT } from "../i18n";
import { isWorkspaceSlugConflict, nameToWorkspaceSlug } from "../workspace/slug";

interface CliCallbackConfig {
  /** Validated localhost callback URL. */
  url: string;
  /** Opaque state to pass back to CLI. */
  state: string;
}

interface LoginPageProps {
  /** Email sign-in by default; signup also collects the initial profile/workspace. */
  mode?: "login" | "signup";
  /** Invite signups create a user profile, then return to the invited team. */
  registrationContext?: "company" | "invitation";
  /** Email resolved from an invitation link before the form renders. */
  initialEmail?: string;
  /** Prevent an invitee from registering a different identity than the invite target. */
  emailLocked?: boolean;
  /** Logo element rendered above the title. */
  logo?: ReactNode;
  /** Called after successful authentication and any signup setup. */
  onSuccess: () => void;
  /** CLI callback config for authorizing CLI tools. Login mode only. */
  cliCallback?: CliCallbackConfig;
  /** Called after a token is obtained (for example, to set cookies). */
  onTokenObtained?: () => void;
  /** Slot rendered at the bottom of the card. */
  extra?: ReactNode;
}

function redirectToCliCallback(url: string, token: string, state: string) {
  const separator = url.includes("?") ? "&" : "?";
  window.location.href = `${url}${separator}token=${encodeURIComponent(token)}&state=${encodeURIComponent(state)}`;
}

/**
 * Validate that a CLI callback URL points to a safe host over HTTP.
 * Allows localhost and private/LAN IPs while blocking arbitrary public hosts.
 */
export function validateCliCallback(cliCallback: string): boolean {
  try {
    const cbUrl = new URL(cliCallback);
    if (cbUrl.protocol !== "http:") return false;
    const h = cbUrl.hostname;
    if (h === "localhost" || h === "127.0.0.1") return true;
    if (/^10\./.test(h)) return true;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(h)) return true;
    if (/^192\.168\./.test(h)) return true;
    return false;
  } catch {
    return false;
  }
}

async function createCompanyWorkspace(
  companyName: string,
  userID: string,
): Promise<Workspace> {
  const baseSlug = nameToWorkspaceSlug(companyName) || "workspace";
  try {
    return await api.createWorkspace({ name: companyName, slug: baseSlug });
  } catch (error) {
    if (!isWorkspaceSlugConflict(error)) throw error;
    return api.createWorkspace({
      name: companyName,
      slug: `${baseSlug}-${userID.replaceAll("-", "").slice(0, 6).toLowerCase()}`,
    });
  }
}

export function LoginPage({
  mode = "login",
  registrationContext = "company",
  initialEmail,
  emailLocked = false,
  logo,
  onSuccess,
  cliCallback,
  onTokenObtained,
  extra,
}: LoginPageProps) {
  const { t } = useT("auth");
  const qc = useQueryClient();
  const isSignup = mode === "signup";
  const isInvitationSignup = isSignup && registrationContext === "invitation";
  const [step, setStep] = useState<"email" | "code" | "cli_confirm">("email");
  const [email, setEmail] = useState(() => initialEmail?.trim().toLowerCase() ?? "");
  const [code, setCode] = useState("");
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [companyName, setCompanyName] = useState("");
  const [profileDescription, setProfileDescription] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [existingUser, setExistingUser] = useState<User | null>(null);
  const verifiedSignupUserRef = useRef<User | null>(null);
  const authSourceRef = useRef<"cookie" | "localStorage">("cookie");

  useEffect(() => {
    const normalized = initialEmail?.trim().toLowerCase();
    if (normalized) setEmail(normalized);
  }, [initialEmail]);

  useEffect(() => {
    if (!cliCallback || isSignup) return;
    api.setToken(null);
    api
      .getMe()
      .then((user) => {
        authSourceRef.current = "cookie";
        setExistingUser(user);
        setStep("cli_confirm");
      })
      .catch(() => {
        const token = localStorage.getItem("agora_token");
        if (!token) return;
        api.setToken(token);
        api
          .getMe()
          .then((user) => {
            authSourceRef.current = "localStorage";
            setExistingUser(user);
            setStep("cli_confirm");
          })
          .catch(() => {
            api.setToken(null);
            localStorage.removeItem("agora_token");
          });
      });
  }, [cliCallback, isSignup]);

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown((value) => value - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  const signupFieldsComplete =
    firstName.trim().length > 0 &&
    lastName.trim().length > 0 &&
    (isInvitationSignup || companyName.trim().length > 0);

  const handleSendCode = useCallback(
    async (event?: React.FormEvent) => {
      event?.preventDefault();
      if (!email) {
        setError(t(($) => $.common.email_required));
        return;
      }
      if (isSignup && !signupFieldsComplete) {
        setError(t(($) => $.signup.required_fields));
        return;
      }
      setLoading(true);
      setError("");
      try {
        await useAuthStore.getState().sendCode(email, mode);
        setStep("code");
        setCode("");
        setCooldown(60);
      } catch (err) {
        setError(
          err instanceof Error
            ? err.message
            : `${t(($) => $.errors.send_failed)} ${t(($) => $.errors.server_unreachable)}`,
        );
      } finally {
        setLoading(false);
      }
    },
    [email, isSignup, mode, signupFieldsComplete, t],
  );

  const finishSignup = useCallback(
    async (user: User) => {
      const updatedUser = await api.updateMe({
        name: `${firstName.trim()} ${lastName.trim()}`,
        profile_description: profileDescription.trim(),
      });
      useAuthStore.getState().setUser(updatedUser);
      if (isInvitationSignup) {
        const workspaces = await api.listWorkspaces();
        qc.setQueryData(workspaceKeys.list(), workspaces);
      } else {
        const workspace = await createCompanyWorkspace(companyName.trim(), user.id);
        qc.setQueryData(workspaceKeys.list(), [workspace]);
      }
      onTokenObtained?.();
      onSuccess();
    }, [companyName, firstName, isInvitationSignup, lastName, onSuccess, onTokenObtained, profileDescription, qc]);

  const handleVerify = useCallback(
    async (value: string) => {
      if (value.length !== 6) return;
      setLoading(true);
      setError("");
      try {
        if (isSignup) {
          const user =
            verifiedSignupUserRef.current ??
            (await useAuthStore.getState().verifyCode(email, value, "signup"));
          verifiedSignupUserRef.current = user;
          await finishSignup(user);
          return;
        }

        if (cliCallback) {
          const { token } = await api.verifyCode(email, value, "login");
          localStorage.setItem("agora_token", token);
          api.setToken(token);
          onTokenObtained?.();
          redirectToCliCallback(cliCallback.url, token, cliCallback.state);
          return;
        }

        await useAuthStore.getState().verifyCode(email, value, "login");
        const workspaces = await api.listWorkspaces();
        qc.setQueryData(workspaceKeys.list(), workspaces);
        onTokenObtained?.();
        onSuccess();
      } catch (err) {
        setError(
          err instanceof Error ? err.message : t(($) => $.errors.code_invalid),
        );
        if (!verifiedSignupUserRef.current) setCode("");
        setLoading(false);
      }
    }, [cliCallback, email, finishSignup, isSignup, onSuccess, onTokenObtained, qc, t]);

  const handleResend = async () => {
    if (cooldown > 0) return;
    setError("");
    try {
      await useAuthStore.getState().sendCode(email, mode);
      setCooldown(60);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : t(($) => $.errors.resend_failed),
      );
    }
  };

  const handleCliAuthorize = async () => {
    if (!cliCallback) return;
    setLoading(true);
    try {
      let token: string;
      if (authSourceRef.current === "localStorage") {
        const stored = localStorage.getItem("agora_token");
        if (!stored) throw new Error("token missing");
        token = stored;
      } else {
        token = (await api.issueCliToken()).token;
      }
      onTokenObtained?.();
      redirectToCliCallback(cliCallback.url, token, cliCallback.state);
    } catch {
      setError(t(($) => $.errors.cli_auth_failed));
      setExistingUser(null);
      setStep("email");
      setLoading(false);
    }
  };

  if (step === "cli_confirm" && existingUser) {
    return (
      <div className="flex min-h-svh items-center justify-center px-4">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            {logo && <div className="mx-auto mb-4">{logo}</div>}
            <CardTitle className="text-2xl">{t(($) => $.cli.title)}</CardTitle>
            <CardDescription>
              {t(($) => $.cli.description, { email: existingUser.email })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <Button onClick={handleCliAuthorize} disabled={loading} className="w-full" size="lg">
              {loading ? t(($) => $.cli.authorizing) : t(($) => $.cli.authorize)}
            </Button>
            <Button
              variant="ghost"
              className="w-full"
              onClick={() => {
                setExistingUser(null);
                setStep("email");
              }}
            >
              {t(($) => $.cli.different_account)}
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (step === "code") {
    return (
      <div className="flex min-h-svh items-center justify-center px-4">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            {logo && <div className="mx-auto mb-4">{logo}</div>}
            <CardTitle className="text-2xl">{t(($) => $.verify.title)}</CardTitle>
            <CardDescription>
              {t(($) => $.verify.description, { email })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col items-center gap-4">
            <InputOTP
              maxLength={6}
              value={code}
              onChange={(value) => {
                setCode(value);
                if (value.length === 6) void handleVerify(value);
              }}
              disabled={loading}
            >
              <InputOTPGroup>
                {Array.from({ length: 6 }, (_, index) => (
                  <InputOTPSlot key={index} index={index} />
                ))}
              </InputOTPGroup>
            </InputOTP>
            {error && <p className="text-center text-sm text-destructive">{error}</p>}
            {verifiedSignupUserRef.current && error ? (
              <Button type="button" variant="outline" onClick={() => void handleVerify(code)} disabled={loading}>
                {t(($) => $.signup.retry_setup)}
              </Button>
            ) : null}
            <button
              type="button"
              onClick={handleResend}
              disabled={cooldown > 0}
              className="text-sm text-primary underline-offset-4 hover:underline disabled:cursor-not-allowed disabled:text-muted-foreground disabled:no-underline"
            >
              {cooldown > 0
                ? t(($) => $.verify.resend_cooldown, { seconds: cooldown })
                : t(($) => $.verify.resend)}
            </button>
          </CardContent>
          <CardFooter>
            <Button
              type="button"
              variant="ghost"
              className="w-full"
              onClick={() => {
                setStep("email");
                setCode("");
                setError("");
              }}
            >
              {t(($) => $.common.back)}
            </Button>
          </CardFooter>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-svh items-center justify-center px-4 py-8">
      <Card className={`w-full ${isSignup ? "max-w-lg" : "max-w-sm"}`}>
        <CardHeader className="text-center">
          {logo && <div className="mx-auto mb-4">{logo}</div>}
          <CardTitle className="text-2xl">
            {isSignup ? t(($) => $.signup.title) : t(($) => $.signin.title)}
          </CardTitle>
          <CardDescription>
            {isInvitationSignup
              ? t(($) => $.signup.invitation_description)
              : isSignup
                ? t(($) => $.signup.description)
                : t(($) => $.signin.description)}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <form id={`${mode}-form`} onSubmit={handleSendCode} className="space-y-4">
            {isSignup ? (
              <>
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="space-y-2">
                    <Label htmlFor="signup-first-name">{t(($) => $.signup.first_name)}</Label>
                    <Input
                      id="signup-first-name"
                      value={firstName}
                      onChange={(event) => setFirstName(event.target.value)}
                      autoComplete="given-name"
                      autoFocus
                      required
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="signup-last-name">{t(($) => $.signup.last_name)}</Label>
                    <Input
                      id="signup-last-name"
                      value={lastName}
                      onChange={(event) => setLastName(event.target.value)}
                      autoComplete="family-name"
                      required
                    />
                  </div>
                </div>
                {!isInvitationSignup ? (
                  <div className="space-y-2">
                    <Label htmlFor="signup-company">{t(($) => $.signup.company_name)}</Label>
                    <Input
                      id="signup-company"
                      value={companyName}
                      onChange={(event) => setCompanyName(event.target.value)}
                      autoComplete="organization"
                      required
                    />
                  </div>
                ) : (
                  <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm text-muted-foreground">
                    {t(($) => $.signup.invitation_hint)}
                  </p>
                )}
              </>
            ) : null}
            <div className="space-y-2">
              <Label htmlFor={`${mode}-email`}>{t(($) => $.common.email)}</Label>
              <Input
                id={`${mode}-email`}
                type="email"
                placeholder={t(($) => $.common.email_placeholder)}
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                disabled={emailLocked}
                autoComplete="email"
                autoFocus={!isSignup}
                required
              />
            </div>
            {isSignup ? (
              <div className="space-y-2">
                <Label htmlFor="signup-profile">{t(($) => $.signup.about_you)}</Label>
                <Textarea
                  id="signup-profile"
                  value={profileDescription}
                  onChange={(event) => setProfileDescription(event.target.value)}
                  placeholder={t(($) => $.signup.about_you_placeholder)}
                  rows={3}
                  maxLength={2000}
                />
                <p className="text-xs text-muted-foreground">{t(($) => $.signup.about_you_hint)}</p>
              </div>
            ) : null}
            {error && <p className="text-sm text-destructive">{error}</p>}
          </form>
          <Button
            type="submit"
            form={`${mode}-form`}
            className="w-full"
            size="lg"
            disabled={!email || (isSignup && !signupFieldsComplete) || loading}
          >
            {loading
              ? t(($) => $.signin.sending)
              : isSignup
                ? t(($) => $.signup.continue)
                : t(($) => $.signin.continue)}
          </Button>
          {extra && <div className="w-full pt-1 text-center">{extra}</div>}
        </CardContent>
      </Card>
    </div>
  );
}
