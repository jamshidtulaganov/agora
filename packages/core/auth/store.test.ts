import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "../api/client";
import { ApiError } from "../api/client";
import type { StorageAdapter, User } from "../types";
import { createAuthStore } from "./store";

const fakeUser: User = {
  id: "u1",
  name: "Alice",
  email: "alice@example.com",
  avatar_url: null,
} as User;

function makeStorage(initial: Record<string, string> = {}): StorageAdapter & {
  snapshot: () => Record<string, string>;
} {
  const data = { ...initial };
  return {
    getItem: (k) => data[k] ?? null,
    setItem: (k, v) => {
      data[k] = v;
    },
    removeItem: (k) => {
      delete data[k];
    },
    snapshot: () => ({ ...data }),
  };
}

function makeApi(getMe: () => Promise<User>): ApiClient & {
  logout: ReturnType<typeof vi.fn>;
} {
  return {
    setToken: vi.fn(),
    getMe,
    logout: vi.fn(() => Promise.resolve()),
    // Only the methods touched by the store are needed. Cast to
    // ApiClient for type compatibility — the store treats it opaquely.
  } as unknown as ApiClient & { logout: ReturnType<typeof vi.fn> };
}

describe("authStore.initialize — token mode", () => {
  it("keeps the stored token when getMe fails with a non-401 ApiError (e.g. 500)", async () => {
    const storage = makeStorage({ agora_token: "t" });
    const api = makeApi(() =>
      Promise.reject(new ApiError("server error", 500, "Internal Server Error")),
    );
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toBeNull();
    expect(store.getState().isLoading).toBe(false);
    expect(storage.snapshot().agora_token).toBe("t");
  });

  it("keeps the stored token on a network failure (non-ApiError throw)", async () => {
    const storage = makeStorage({ agora_token: "t" });
    const api = makeApi(() => Promise.reject(new TypeError("fetch failed")));
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toBeNull();
    expect(storage.snapshot().agora_token).toBe("t");
  });

  it("on 401, leaves storage cleanup to ApiClient.onUnauthorized and resets state", async () => {
    // Simulate the real path: ApiClient fires onUnauthorized on 401, which
    // removes the token from storage. The store's catch block must not
    // duplicate or short-circuit this — it should only reset in-memory
    // auth state.
    const storage = makeStorage({ agora_token: "t" });
    const api = makeApi(() => {
      storage.removeItem("agora_token"); // stand-in for onUnauthorized
      return Promise.reject(new ApiError("unauthorized", 401, "Unauthorized"));
    });
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toBeNull();
    expect(storage.snapshot().agora_token).toBeUndefined();
  });

  it("populates user when getMe succeeds", async () => {
    const storage = makeStorage({ agora_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });

    await store.getState().initialize();

    expect(store.getState().user).toEqual(fakeUser);
    expect(storage.snapshot().agora_token).toBe("t");
  });
});

describe("authStore.logout", () => {
  it("calls api.logout even in token mode — a session can hold both a localStorage token and the HttpOnly cookie (e.g. Telegram login), and clearing only one lets the other re-authenticate on reload", () => {
    const storage = makeStorage({ agora_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });

    store.getState().logout();

    expect(api.logout).toHaveBeenCalledTimes(1);
    expect(storage.snapshot().agora_token).toBeUndefined();
    expect(api.setToken).toHaveBeenCalledWith(null);
    expect(store.getState().user).toBeNull();
  });

  it("calls api.logout in cookie mode and clears any leftover legacy token", () => {
    const storage = makeStorage({ agora_token: "leftover" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage, cookieAuth: true });

    store.getState().logout();

    expect(api.logout).toHaveBeenCalledTimes(1);
    expect(storage.snapshot().agora_token).toBeUndefined();
  });

  it("survives api.logout rejection (fire-and-forget)", async () => {
    const storage = makeStorage({ agora_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    (api.logout as ReturnType<typeof vi.fn>).mockReturnValue(
      Promise.reject(new ApiError("unauthorized", 401, "Unauthorized")),
    );
    const store = createAuthStore({ api, storage });

    store.getState().logout();
    await Promise.resolve();

    expect(store.getState().user).toBeNull();
    expect(storage.snapshot().agora_token).toBeUndefined();
  });
});

describe("authStore.loginWithToken", () => {
  it("does NOT persist the token to storage in cookie mode — the verify endpoint already set the HttpOnly cookie; writing agora_token would flip the next session into legacy token mode with split credentials", async () => {
    const storage = makeStorage();
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage, cookieAuth: true });

    await store.getState().loginWithToken("jwt");

    expect(storage.snapshot().agora_token).toBeUndefined();
    expect(api.setToken).toHaveBeenCalledWith("jwt");
    expect(store.getState().user).toEqual(fakeUser);
  });

  it("persists the token in token mode (Electron / Telegram Mini App)", async () => {
    const storage = makeStorage();
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });

    await store.getState().loginWithToken("jwt");

    expect(storage.snapshot().agora_token).toBe("jwt");
    expect(store.getState().user).toEqual(fakeUser);
  });
});
