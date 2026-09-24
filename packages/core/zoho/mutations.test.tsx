/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { useDisconnectZohoAccount } from "./mutations";
import { zohoAccountKeys } from "./queries";
import { EMPTY_ZOHO_ACCOUNT, type ZohoAccount } from "./types";

const CONNECTED: ZohoAccount = {
  available: true,
  connected: true,
  status: "connected",
  email: "shohruh.a@octanefuel.com",
  name: "Shohruh A.",
  crm_role: "Collections Agent",
  crm_profile: "Standard",
  desk_departments: ["Collections"],
  checked_at: "2026-09-25T10:00:00Z",
};

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe("useDisconnectZohoAccount", () => {
  let qc: QueryClient;
  let disconnectZohoAccount: ReturnType<typeof vi.fn<() => Promise<void>>>;
  let getMyZohoAccount: ReturnType<typeof vi.fn<() => Promise<ZohoAccount>>>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    disconnectZohoAccount = vi.fn();
    getMyZohoAccount = vi.fn();
    setApiInstance({
      disconnectZohoAccount,
      getMyZohoAccount,
    } as unknown as ApiClient);
    qc.setQueryData(zohoAccountKeys.mine(), CONNECTED);
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("flips the cache to not connected before the request settles", async () => {
    let resolve!: () => void;
    disconnectZohoAccount.mockReturnValue(
      new Promise<void>((r) => {
        resolve = r;
      }),
    );
    const { result } = renderHook(() => useDisconnectZohoAccount(), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate();
    });

    await waitFor(() =>
      expect(qc.getQueryData(zohoAccountKeys.mine())).toEqual({
        ...EMPTY_ZOHO_ACCOUNT,
        available: true,
      }),
    );
    expect(disconnectZohoAccount).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolve();
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });

  it("rolls back to the connected account when the request fails", async () => {
    disconnectZohoAccount.mockRejectedValue(new Error("boom"));
    const { result } = renderHook(() => useDisconnectZohoAccount(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync().catch(() => undefined);
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(qc.getQueryData(zohoAccountKeys.mine())).toEqual(CONNECTED);
  });

  it("invalidates the account query on settle", async () => {
    disconnectZohoAccount.mockResolvedValue(undefined);
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => useDisconnectZohoAccount(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync();
    });

    expect(invalidate).toHaveBeenCalledWith({
      queryKey: zohoAccountKeys.mine(),
    });
  });
});
