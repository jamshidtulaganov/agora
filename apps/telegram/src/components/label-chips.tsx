import type { Label } from "@agora/core/types";

// Render an issue's labels (the bot wizard's "type") as colored chips, tinted
// from each label's hex color. Members set them via the bot / web; this makes
// them visible in the Mini App.
export function LabelChips({ labels, max }: { labels?: Label[]; max?: number }) {
  if (!labels?.length) return null;
  const shown = max ? labels.slice(0, max) : labels;
  return (
    <>
      {shown.map((l) => (
        <span
          key={l.id}
          className="inline-flex shrink-0 items-center rounded-full px-1.5 py-0.5 text-[11px] font-medium"
          style={{ backgroundColor: `${l.color}1f`, color: l.color }}
        >
          {l.name}
        </span>
      ))}
    </>
  );
}

// Compact colored dots (one per label) for tight list rows.
export function LabelDots({ labels, max = 3 }: { labels?: Label[]; max?: number }) {
  if (!labels?.length) return null;
  return (
    <span className="flex shrink-0 items-center gap-0.5">
      {labels.slice(0, max).map((l) => (
        <span
          key={l.id}
          className="size-1.5 rounded-full"
          style={{ backgroundColor: l.color }}
        />
      ))}
    </span>
  );
}
