import { useEffect, useRef, useState } from "react";
import { useAuthStore } from "@agora/core/auth";
import { getApi } from "@agora/core/api";
import { getInitData } from "../telegram/sdk";

export type AuthStatus = "loading" | "authed" | "error";

export interface TelegramAuthState {
  status: AuthStatus;
  /** "not-telegram" when opened outside Telegram; "auth-failed" on a 401/网络. */
  reason: "not-telegram" | "auth-failed" | null;
  retry: () => void;
}

// Drives Mini App authentication on top of CoreProvider's token mode.
//
// CoreProvider's AuthInitializer runs first: if a valid token is already in
// localStorage (returning user) it hydrates the session and `user` is set. If
// not (first open, or expired/cleared token), it finishes with `user === null`
// and `isLoading === false` — that's our cue to exchange the signed initData for
// a fresh session via POST /auth/telegram/miniapp, then loginWithToken() to
// populate the auth store exactly like every other login path.
export function useTelegramAuth(): TelegramAuthState {
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const [reason, setReason] = useState<TelegramAuthState["reason"]>(null);
  const [nonce, setNonce] = useState(0);
  const inFlight = useRef(false);

  useEffect(() => {
    // Wait for AuthInitializer's boot check to settle.
    if (isLoading || user || inFlight.current) return;

    const initData = getInitData();
    if (!initData) {
      setReason("not-telegram");
      return;
    }

    inFlight.current = true;
    setReason(null);
    (async () => {
      try {
        const { token } = await getApi().telegramMiniAppLogin(initData);
        await useAuthStore.getState().loginWithToken(token);
      } catch {
        setReason("auth-failed");
      } finally {
        inFlight.current = false;
      }
    })();
    // nonce re-triggers a manual retry.
  }, [isLoading, user, nonce]);

  if (user) return { status: "authed", reason: null, retry };
  if (reason) return { status: "error", reason, retry };
  return { status: "loading", reason: null, retry };

  function retry() {
    setReason(null);
    setNonce((n) => n + 1);
  }
}
