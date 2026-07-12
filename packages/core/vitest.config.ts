import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    globals: true,
    include: ["**/*.test.{ts,tsx}"],
    passWithNoTests: true,
    // Fuzz suites run 2k–10k adversarial iterations in a synchronous loop,
    // which the default 5s timeout can't accommodate on a slower CI runner
    // (a sync loop can't be interrupted mid-iteration anyway — the timeout
    // only fires after it finishes). 60s gives the property tests headroom.
    testTimeout: 60_000,
  },
});
