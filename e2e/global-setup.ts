import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { TestApiClient } from "./fixtures";

// Logs in the shared default E2E user ONCE and stashes the token for every
// worker. Per-test logins re-ran send-code for the same email from parallel
// workers and tripped the backend's per-email 60s cooldown (429).
const DEFAULT_E2E_NAME = "E2E User";
const DEFAULT_E2E_EMAIL = "e2e@agora.dev";
const DEFAULT_E2E_WORKSPACE = "e2e-workspace";
const DEFAULT_E2E_WORKSPACE_NAME = "E2E Workspace";

export const AUTH_STATE_PATH = join(
  dirname(fileURLToPath(import.meta.url)),
  ".auth",
  "default.json",
);

export default async function globalSetup() {
  const api = new TestApiClient();
  await api.login(DEFAULT_E2E_EMAIL, DEFAULT_E2E_NAME);
  const workspace = await api.ensureWorkspace(
    DEFAULT_E2E_WORKSPACE_NAME,
    DEFAULT_E2E_WORKSPACE,
  );
  mkdirSync(dirname(AUTH_STATE_PATH), { recursive: true });
  writeFileSync(
    AUTH_STATE_PATH,
    JSON.stringify({ token: api.getToken(), slug: workspace.slug }),
  );
}
