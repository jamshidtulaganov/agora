import { cn } from "../lib/cn";

// Shimmering placeholder blocks shown while a tab's queries load, matching
// the design's skeleton spec: title bar + 3 progressively-faded cards.

function Block({ className, delay }: { className?: string; delay?: number }) {
  return (
    <span
      className={cn("block animate-ag-shimmer rounded-md bg-muted", className)}
      style={delay ? { animationDelay: `${delay}s` } : undefined}
    />
  );
}

function SkeletonCard({ opacity, stagger }: { opacity: number; stagger: number }) {
  return (
    <div
      className="flex flex-col gap-[11px] rounded-xl border border-border bg-card px-4 py-[15px]"
      style={{ opacity }}
    >
      <div className="flex items-center justify-between">
        <Block className="h-3 w-[74px]" delay={stagger} />
        <Block className="h-[18px] w-14 rounded-full" delay={stagger + 0.1} />
      </div>
      <Block className="h-[15px]" delay={stagger + 0.2} />
      <div className="flex items-center gap-2.5">
        <Block className="h-2 flex-1 rounded" delay={stagger + 0.3} />
        <Block className="size-[26px] rounded-full" delay={stagger + 0.4} />
      </div>
    </div>
  );
}

export function TabSkeleton() {
  return (
    <div className="flex animate-ag-fade-in flex-col gap-2.5 px-4 py-2.5">
      <Block className="mx-1 mb-2 mt-1 h-6 w-[110px] rounded-lg" />
      <SkeletonCard opacity={1} stagger={0} />
      <SkeletonCard opacity={0.7} stagger={0.2} />
      <SkeletonCard opacity={0.4} stagger={0.4} />
    </div>
  );
}
