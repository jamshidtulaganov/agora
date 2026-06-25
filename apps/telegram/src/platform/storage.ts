import type { StorageAdapter } from "@agora/core/types/storage";

// Synchronous localStorage-backed StorageAdapter for CoreProvider token mode.
// Telegram's in-app browser supports localStorage; the JWT lives under
// "agora_token" (the key CoreProvider/AuthInitializer read). Wrapped in
// try/catch so a storage-disabled webview degrades to in-memory-less rather
// than throwing on boot. CloudStorage (async) is a future upgrade — see plan.
export const localStorageAdapter: StorageAdapter = {
  getItem(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  setItem(key: string, value: string): void {
    try {
      localStorage.setItem(key, value);
    } catch {
      // ignore (private mode / disabled storage)
    }
  },
  removeItem(key: string): void {
    try {
      localStorage.removeItem(key);
    } catch {
      // ignore
    }
  },
};

export const TOKEN_KEY = "agora_token";
