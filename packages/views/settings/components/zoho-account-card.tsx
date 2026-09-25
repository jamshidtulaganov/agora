"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { TriangleAlert } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@agora/ui/components/ui/alert-dialog";
import { ApiError } from "@agora/core/api";
import {
  EMPTY_ZOHO_ACCOUNT,
  myZohoAccountOptions,
  useConnectZohoAccount,
  useDisconnectZohoAccount,
  zohoAccountState,
} from "@agora/core/zoho";
import { useT } from "../../i18n";
import { openExternal } from "../../platform";

/**
 * The person's own Zoho account, shown under the workspace's Zoho connector.
 * Connected once and used in every workspace; connecting goes through this
 * workspace's connector. It opens Zoho's sign-in page in the system browser;
 * the card refetches when the window regains focus, so it flips to
 * "connected" as soon as the person comes back.
 */
export function ZohoAccountCard({ wsId }: { wsId: string }) {
  const { t } = useT("settings");
  const { data, isError, refetch } = useQuery({
    ...myZohoAccountOptions(wsId),
    enabled: !!wsId,
  });
  const connectMut = useConnectZohoAccount(wsId);
  const disconnectMut = useDisconnectZohoAccount(wsId);
  const [confirmOpen, setConfirmOpen] = useState(false);

  // TanStack Query only listens for `visibilitychange`, which never fires on
  // desktop while the person is over in the browser (the app window stays
  // visible). Window focus covers that return trip.
  useEffect(() => {
    const onFocus = () => {
      void refetch({ cancelRefetch: false });
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [refetch]);

  // A load that failed for good (e.g. a server without this endpoint) reads
  // as "not available yet" instead of an empty card.
  const account = data ?? (isError ? EMPTY_ZOHO_ACCOUNT : undefined);
  const state = account ? zohoAccountState(account) : null;

  const connect = () => {
    if (connectMut.isPending) return;
    connectMut.mutate(undefined, {
      onSuccess: ({ url }) => openExternal(url),
      onError: (e) => {
        // 409: the workspace's connector went away since the card loaded.
        // Say it in the person's language and refresh the card to match.
        if (e instanceof ApiError && e.status === 409) {
          toast.error(t(($) => $.zoho.account.not_available));
          void refetch({ cancelRefetch: false });
          return;
        }
        toast.error(
          e instanceof ApiError && e.message
            ? e.message
            : t(($) => $.zoho.account.error_connect_failed),
        );
      },
    });
  };

  const disconnect = () => {
    setConfirmOpen(false);
    disconnectMut.mutate(undefined, {
      onError: (e) => {
        toast.error(
          e instanceof ApiError && e.message
            ? e.message
            : t(($) => $.zoho.account.error_disconnect_failed),
        );
      },
    });
  };

  const identity = account ? account.email || account.name : "";
  const crmRole = account?.crm_role?.trim() ?? "";
  const crmProfile = account?.crm_profile?.trim() ?? "";
  const departments = Array.isArray(account?.desk_departments)
    ? account.desk_departments
    : [];
  const details: string[] = [];
  if (crmRole && crmProfile) {
    details.push(
      t(($) => $.zoho.account.crm_role_and_profile, {
        role: crmRole,
        profile: crmProfile,
      }),
    );
  } else if (crmRole || crmProfile) {
    details.push(
      t(($) => $.zoho.account.crm_single, { value: crmRole || crmProfile }),
    );
  }
  if (departments.length > 0) {
    details.push(
      t(($) => $.zoho.account.desk, { departments: departments.join(", ") }),
    );
  }

  // Connecting needs this workspace's Zoho connector; removing an existing
  // connection does not.
  const showConnect =
    (state === "not_connected" || state === "reconnect") &&
    account?.available === true;
  const showDisconnect = state === "connected" || state === "reconnect";
  const connectLabel = connectMut.isPending
    ? t(($) => $.zoho.account.connecting)
    : state === "reconnect"
      ? t(($) => $.zoho.account.reconnect)
      : t(($) => $.zoho.account.connect);

  return (
    <Card>
      <CardContent className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0 space-y-1">
          <h3 className="text-sm font-medium">{t(($) => $.zoho.account.title)}</h3>

          {state === "unavailable" && (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.zoho.account.not_available)}
            </p>
          )}

          {state !== null && state !== "unavailable" && (
            <p className="max-w-2xl text-xs text-muted-foreground">
              {t(($) => $.zoho.account.description)}
            </p>
          )}

          {state === "connected" && (
            <>
              <p className="truncate text-sm">
                {identity
                  ? t(($) => $.zoho.account.connected_as, { email: identity })
                  : t(($) => $.zoho.account.connected)}
              </p>
              {details.length > 0 && (
                <p className="text-xs text-muted-foreground">
                  {details.join(" · ")}
                </p>
              )}
            </>
          )}

          {state === "reconnect" && (
            <p className="flex items-start gap-1.5 text-xs">
              <TriangleAlert
                className="mt-px size-3.5 shrink-0 text-warning"
                aria-hidden="true"
              />
              <span>{t(($) => $.zoho.account.reconnect_warning)}</span>
            </p>
          )}
        </div>

        {(showConnect || showDisconnect) && (
          <div className="flex shrink-0 items-center gap-2">
            {showConnect && (
              <Button size="sm" onClick={connect} disabled={connectMut.isPending}>
                {connectLabel}
              </Button>
            )}
            {showDisconnect && (
              <Button
                size="sm"
                variant={state === "connected" ? "outline" : "ghost"}
                onClick={() => setConfirmOpen(true)}
                disabled={disconnectMut.isPending}
              >
                {t(($) => $.zoho.account.disconnect)}
              </Button>
            )}
          </div>
        )}

        <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t(($) => $.zoho.account.disconnect_confirm_title)}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.zoho.account.disconnect_confirm_description)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>
                {t(($) => $.zoho.account.cancel)}
              </AlertDialogCancel>
              <AlertDialogAction onClick={disconnect}>
                {t(($) => $.zoho.account.disconnect)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
