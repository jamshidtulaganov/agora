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
 * The person's own Zoho account, connected once and used in every workspace.
 * Connecting opens Zoho's sign-in page in the system browser; the card
 * refetches when the window regains focus, so it flips to "connected" as soon
 * as the person comes back.
 */
export function ZohoAccountCard() {
  const { t } = useT("settings");
  const { data, isError, refetch } = useQuery(myZohoAccountOptions());
  const connectMut = useConnectZohoAccount();
  const disconnectMut = useDisconnectZohoAccount();
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
  // as "not set up here" instead of an empty card.
  const account = data ?? (isError ? EMPTY_ZOHO_ACCOUNT : undefined);
  const state = account ? zohoAccountState(account) : null;

  const connect = () => {
    if (connectMut.isPending) return;
    connectMut.mutate(undefined, {
      onSuccess: ({ url }) => openExternal(url),
      onError: (e) => {
        toast.error(
          e instanceof ApiError && e.message
            ? e.message
            : t(($) => $.account.zoho.error_connect_failed),
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
            : t(($) => $.account.zoho.error_disconnect_failed),
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
      t(($) => $.account.zoho.crm_role_and_profile, {
        role: crmRole,
        profile: crmProfile,
      }),
    );
  } else if (crmRole || crmProfile) {
    details.push(
      t(($) => $.account.zoho.crm_single, { value: crmRole || crmProfile }),
    );
  }
  if (departments.length > 0) {
    details.push(
      t(($) => $.account.zoho.desk, { departments: departments.join(", ") }),
    );
  }

  // Reconnecting needs the server's Zoho sign-in to be set up; removing an
  // existing connection does not.
  const showConnect =
    (state === "not_connected" || state === "reconnect") &&
    account?.available === true;
  const showDisconnect = state === "connected" || state === "reconnect";
  const connectLabel = connectMut.isPending
    ? t(($) => $.account.zoho.connecting)
    : state === "reconnect"
      ? t(($) => $.account.zoho.reconnect)
      : t(($) => $.account.zoho.connect);

  return (
    <Card>
      <CardContent className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0 space-y-1">
          <h3 className="text-sm font-medium">{t(($) => $.account.zoho.title)}</h3>

          {state === "unavailable" && (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.account.zoho.not_available)}
            </p>
          )}

          {state === "not_connected" && (
            <p className="max-w-2xl text-xs text-muted-foreground">
              {t(($) => $.account.zoho.description)}
            </p>
          )}

          {state === "connected" && (
            <>
              <p className="truncate text-sm">
                {identity
                  ? t(($) => $.account.zoho.connected_as, { email: identity })
                  : t(($) => $.account.zoho.connected)}
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
              <span>{t(($) => $.account.zoho.reconnect_warning)}</span>
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
                {t(($) => $.account.zoho.disconnect)}
              </Button>
            )}
          </div>
        )}

        <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t(($) => $.account.zoho.disconnect_confirm_title)}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.account.zoho.disconnect_confirm_description)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>
                {t(($) => $.account.zoho.cancel)}
              </AlertDialogCancel>
              <AlertDialogAction onClick={disconnect}>
                {t(($) => $.account.zoho.disconnect)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
