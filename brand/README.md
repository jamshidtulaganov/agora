# Agora brand assets

The Agora mark is an **assembly**: a ring of participants gathered around a shared center.
Three filled nodes (people) + three outlined nodes (agents) circle a solid center node
(the shared work). It replaces the old 8‑point star, which read too close to a generic
AI "sparkle".

## Colors

| Token            | Hex       | Use                                            |
|------------------|-----------|------------------------------------------------|
| Ultramarine      | `#2347E8` | Brand primary — center node, app tile, CTAs    |
| Ultramarine (dk) | `#4F7BF5` | Center node on dark backgrounds (contrast pop) |
| Ink              | `#18181B` | Mark + wordmark on light                       |
| Paper            | `#FAFAF9` | Light background                               |
| Off‑white        | `#F5F5F4` | Mark + wordmark on dark                        |
| Surface (dark)   | `#16171B` | Dark background                                |

## Type

- **Wordmark / display:** Source Serif 4 (medium, lowercase, tracking ‑0.02em) — the in‑app `--font-serif`.
- **UI / taglines:** Inter.

The wordmark in the lockup/banner SVGs references `Source Serif 4`; when rendering to PNG
without that font installed it falls back to Georgia. For pixel‑exact exports, install
Source Serif 4 or convert the wordmark to outlines in a vector tool.

## Files

| File                              | What                                            |
|-----------------------------------|-------------------------------------------------|
| `icon-light.svg` / `icon-dark.svg`| Mark only, transparent (ink / off‑white nodes)  |
| `icon-mono-black.svg` / `-white`  | Single‑color mark (no accent)                   |
| `app-icon.svg`                    | Ultramarine rounded tile + white mark (favicon/PWA source) |
| `lockup-horizontal-{light,dark}`  | Mark + `agora` wordmark, side by side           |
| `social-1200x630.svg`             | Social / `og:image` card                        |
| `github-header-1280x360.svg`      | README / repo header (light)                    |
| `hero-1280x420.svg`               | Dark hero banner                                |
| `png/`                            | Rendered PNGs (favicons, social, banners, icons)|

## Where it ships in‑product

- Web app mark: `packages/ui/components/common/agora-icon.tsx`
- Web favicons: `apps/web/public/{favicon.svg,icon-master.svg,favicon-*.png}`, `apps/web/app/favicon.ico`
- Web social: `apps/web/public/og.svg`
- Docs site: `apps/docs/app/layout.config.tsx`
- Mobile: `apps/mobile/components/brand/agora-logo.tsx`

Keep these in sync with this folder if the mark changes.
