import type { NextConfig } from "next";
import { config } from "dotenv";
import { resolve } from "path";
import { resolveDocsUrl, resolveRemoteApiUrl } from "./config/runtime-urls";
import { createMDX } from "fumadocs-mdx/next";

// Load root .env so REMOTE_API_URL is available to next.config.ts
config({ path: resolve(__dirname, "../../.env") });

const remoteApiUrl = resolveRemoteApiUrl(process.env);
const docsUrl = resolveDocsUrl(process.env);

// Parse hostnames from CORS_ALLOWED_ORIGINS so that Next.js dev server
// allows cross-origin HMR / webpack requests (e.g. from Tailscale IPs).
const allowedDevOrigins = process.env.CORS_ALLOWED_ORIGINS
  ? process.env.CORS_ALLOWED_ORIGINS.split(",")
      .map((origin) => {
        try {
          return new URL(origin.trim()).host;
        } catch {
          return origin.trim();
        }
      })
      .filter(Boolean)
  : undefined;

const nextConfig: NextConfig = {
  ...(process.env.STANDALONE === "true" ? { output: "standalone" as const } : {}),
  transpilePackages: ["@agora/core", "@agora/ui", "@agora/views"],
  // Type-checking runs in CI, not in the production image build. Skipping it
  // avoids the OOM (SIGKILL) the tsc pass hit on the Fly/Depot builder.
  typescript: { ignoreBuildErrors: true },
  // Static generation ("Collecting page data") with 3 parallel workers OOM-killed
  // the memory-constrained builder. Pin to 1 worker to cap peak memory.
  experimental: { cpus: 1 },
  ...(allowedDevOrigins && allowedDevOrigins.length > 0
    ? { allowedDevOrigins }
    : {}),
  images: {
    formats: ["image/avif", "image/webp"],
    qualities: [75, 80, 85],
  },
  async rewrites() {
    return {
      // Run before file-system routes so /docs isn't shadowed by the
      // [workspaceSlug] dynamic segment.
      beforeFiles: [
        {
          source: "/docs",
          destination: `${docsUrl}/docs`,
        },
        {
          source: "/docs/:path*",
          destination: `${docsUrl}/docs/:path*`,
        },
      ],
      afterFiles: [
        {
          source: "/api/:path*",
          destination: `${remoteApiUrl}/api/:path*`,
        },
        {
          source: "/ws",
          destination: `${remoteApiUrl}/ws`,
        },
        {
          source: "/auth/:path*",
          destination: `${remoteApiUrl}/auth/:path*`,
        },
        {
          // Telegram posts bot updates here (backend route: /telegram/webhook).
          source: "/telegram/:path*",
          destination: `${remoteApiUrl}/telegram/:path*`,
        },
        {
          // Live QA browser reverse-proxy (cloud) — backend
          // /browser/proxy/{token}/editor/browser/* carries the CDP screencast
          // (HTTP + WebSocket) from the remote daemon to the Live-testing bay.
          source: "/browser/:path*",
          destination: `${remoteApiUrl}/browser/:path*`,
        },
        {
          // Playwright trace-viewer reverse-proxy (cloud) — backend
          // /trace/proxy/{token}/* serves the show-trace viewer the QA panel
          // iframes. Without this rule the iframe hits the Next 404 (whose
          // frame-ancestors 'none' CSP blanks it) instead of the backend.
          source: "/trace/:path*",
          destination: `${remoteApiUrl}/trace/:path*`,
        },
        {
          source: "/uploads/:path*",
          destination: `${remoteApiUrl}/uploads/:path*`,
        },
        {
          // Backend health/readiness. `agora setup self-host` probes /health,
          // so it must reach the backend, not the Next.js 404 page.
          source: "/health",
          destination: `${remoteApiUrl}/health`,
        },
        {
          source: "/healthz",
          destination: `${remoteApiUrl}/healthz`,
        },
        {
          // Bitrix24 inbound webhook -> backend /bitrix/webhook.
          source: "/bitrix/:path*",
          destination: `${remoteApiUrl}/bitrix/:path*`,
        },
      ],
      fallback: [],
    };
  },
};

// fumadocs-mdx@12 is incompatible with Next 16's Turbopack: its loader fails to
// dynamic-import `.source/source.config.mjs` under the Turbopack Node evaluator
// (see fumadocs#2658). `dev`/`build` scripts pass `--webpack` to opt out.
// Drop the flag once fumadocs-mdx ships a Turbopack-compatible loader.
const withMDX = createMDX() as (config: NextConfig) => NextConfig;

export default withMDX(nextConfig);
