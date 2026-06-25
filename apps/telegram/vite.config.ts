import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Telegram Mini App SPA. Reuses @agora/core (API client, queries, stores, WS)
// and @agora/ui, transpiled directly from source by Vite (Internal Packages
// pattern). The backend has no public IP, so in production an nginx sidecar
// reverse-proxies /api /ws /auth /uploads to sd-agora-backend.internal — the SPA
// is same-origin with its API. In dev, the proxy below points at the local
// backend so `pnpm dev:telegram` talks to `make server` on :8080.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    // Allow Telegram / tunnel (ngrok, cloudflared) hosts to load the dev server.
    host: true,
    allowedHosts: true,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: true },
      "/auth": { target: "http://localhost:8080", changeOrigin: true },
      "/uploads": { target: "http://localhost:8080", changeOrigin: true },
      "/ws": { target: "ws://localhost:8080", ws: true, changeOrigin: true },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
});
