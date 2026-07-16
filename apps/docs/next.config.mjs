import path from "node:path";
import { fileURLToPath } from "node:url";
import { createMDX } from "fumadocs-mdx/next";

// Pin file tracing to the monorepo root: a lockfile in a parent directory
// (common on dev machines) otherwise skews Next's inferred root and the
// standalone output lands under the wrong path.
const repoRoot = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..");

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  // Standalone output for the Docker runtime (same pattern as apps/web).
  ...(process.env.STANDALONE === "true" ? { output: "standalone" } : {}),
  outputFileTracingRoot: repoRoot,
  basePath: "/docs",
  // Visiting http://host/ (outside basePath) would otherwise 404 — redirect
  // to the docs root. basePath: false makes the source and destination
  // literal (not re-prefixed with `/docs`), so the redirect runs before
  // basePath routing kicks in.
  async redirects() {
    return [
      {
        source: "/",
        destination: "/docs",
        basePath: false,
        permanent: false,
      },
    ];
  },
};

export default withMDX(config);
