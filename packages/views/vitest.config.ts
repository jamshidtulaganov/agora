import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
    include: ["**/*.test.{ts,tsx}"],
    // userEvent-heavy suites type character-by-character; on slow CI runners
    // several have blown vitest's 5s default (telegram-tab, compose-resources
    // — CI runs 35448472646, 35454461176) while passing locally in ~1s. The
    // budget is for the machine, not the code: a hung test still fails, just
    // three times later.
    testTimeout: 15000,
  },
});
