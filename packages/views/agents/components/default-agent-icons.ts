// Preset "default" agent avatars offered in the create-agent form so a new
// agent gets a distinct icon in one click, without uploading a file. Each is a
// self-contained SVG data URI — a friendly bot glyph on a colored tile — so it
// needs no upload/network round-trip and renders identically anywhere
// avatar_url is shown (resolvePublicFileUrl passes data: URIs through
// unchanged).

const ICON_COLORS = [
  "#2563EB", // blue (brand)
  "#0F6E56", // teal
  "#534AB7", // purple
  "#BA7517", // amber
  "#C2410C", // coral
  "#3B6D11", // green
  "#A32D2D", // red
  "#475569", // slate
];

// botTile draws a minimal robot head (antenna + rounded head + two knocked-out
// eyes) in white on a colored rounded tile. The eyes are filled with the tile
// color so they read as cut-outs regardless of the background.
function botTile(bg: string): string {
  const svg =
    `<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 128 128'>` +
    `<rect width='128' height='128' rx='28' fill='${bg}'/>` +
    `<circle cx='64' cy='27' r='5' fill='#ffffff'/>` +
    `<rect x='62' y='30' width='4' height='12' fill='#ffffff'/>` +
    `<rect x='37' y='43' width='54' height='44' rx='13' fill='#ffffff'/>` +
    `<circle cx='53' cy='66' r='6' fill='${bg}'/>` +
    `<circle cx='75' cy='66' r='6' fill='${bg}'/>` +
    `</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

export const DEFAULT_AGENT_ICONS: readonly string[] = ICON_COLORS.map(botTile);
