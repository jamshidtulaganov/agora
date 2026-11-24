/* eslint-disable i18next/no-literal-string -- consent copy mirrors the MCP page, which is English-only for now; i18n is a follow-up */
"use client";

import { Check, Loader2, MessageSquare, ShieldCheck } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@agora/core/auth";
import { connectorConsentOptions, useApproveConnectorConsent } from "@agora/core/mcp";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { Button } from "@agora/ui/components/ui/button";
import { Skeleton } from "@agora/ui/components/ui/skeleton";

// Only ever leave this page for an HTTPS URL the server produced.
function go(url: string) {
  if (url.startsWith("https://")) window.location.assign(url);
}

const ACCESS = [
  { icon: Check, text: "See your workspaces, tasks, projects, agents and knowledge" },
  { icon: MessageSquare, text: "Create and change tasks, projects, agents, automations and more, as you" },
  { icon: Check, text: "Only what your role in each workspace allows" },
];

/**
 * Agora's OAuth consent screen for the Claude / ChatGPT connector. The member
 * is already signed in (the route redirects through login); Allow mints a
 * dedicated token and sends the browser back to the connector.
 */
export function ConnectorConsentPage({ request }: { request: string }) {
  const email = useAuthStore((s) => s.user?.email ?? "");
  const consent = useQuery(connectorConsentOptions(request));
  const approve = useApproveConnectorConsent();
  const app = consent.data?.client_name ?? "";
  const failed = request === "" || consent.isError || (consent.isSuccess && app === "");

  return (
    // The root <body> is overflow-hidden; full-page views own their scrolling.
    <div className="flex h-svh overflow-y-auto bg-background">
      <div className="m-auto w-full max-w-md px-4 py-10">
        <div className="mb-6 flex justify-center">
          <AgoraIcon size="lg" />
        </div>
        <div className="rounded-xl border border-border bg-card p-6 shadow-sm">
          {consent.isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-6 w-3/4" />
              <Skeleton className="h-4 w-1/2" />
              <Skeleton className="h-24 w-full" />
            </div>
          ) : failed ? (
            <div className="space-y-2 text-center">
              <h1 className="text-lg font-semibold">This connection link expired</h1>
              <p className="text-sm text-muted-foreground">
                Go back to Claude or ChatGPT and connect Agora again.
              </p>
            </div>
          ) : (
            <>
              <h1 className="text-lg font-semibold leading-snug">
                {app} wants to access your Agora account
              </h1>
              {email && (
                <p className="mt-1 truncate text-sm text-muted-foreground">Signed in as {email}</p>
              )}
              <p className="mt-5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                {app} will be able to
              </p>
              <ul className="mt-2 space-y-2.5">
                {ACCESS.map(({ icon: Icon, text }) => (
                  <li key={text} className="flex items-start gap-2.5 text-sm">
                    <Icon className="mt-0.5 size-4 shrink-0 text-brand" aria-hidden="true" />
                    {text}
                  </li>
                ))}
              </ul>
              <p className="mt-4 flex items-start gap-2 rounded-md bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
                <ShieldCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                Deleting or removing anything waits for you to confirm it here in
                Agora. It sees only the workspaces you belong to.
              </p>
              {approve.isError && (
                <p role="alert" className="mt-4 text-sm text-destructive">
                  Could not connect. Go back to {app} and try again.
                </p>
              )}
              <div className="mt-6 flex gap-2">
                <Button
                  variant="outline"
                  className="flex-1"
                  disabled={approve.isPending}
                  onClick={() => go(consent.data?.cancel_url ?? "")}
                >
                  Cancel
                </Button>
                <Button
                  className="flex-1"
                  disabled={approve.isPending || approve.isSuccess}
                  onClick={() =>
                    approve.mutate(request, { onSuccess: (res) => go(res.redirect_url) })
                  }
                >
                  {(approve.isPending || approve.isSuccess) && <Loader2 className="animate-spin" />}
                  Allow
                </Button>
              </div>
              <p className="mt-4 text-center text-xs text-muted-foreground">
                Stays connected for 90 days. Disconnect any time in Settings → Tokens.
              </p>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
