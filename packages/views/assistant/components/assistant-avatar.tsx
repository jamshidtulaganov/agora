import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { cn } from "@agora/ui/lib/utils";

/**
 * The Agora Assistant's own avatar — the product's assembly mark on a
 * brand-tinted disc, deliberately NOT the violet robot used for workspace
 * agents. The assistant is Agora itself speaking (a system capability, one
 * per instance, not configurable), while agents are workspace-owned actors a
 * team creates and customizes; the two must not be confusable in a transcript
 * or a picker. AgoraIcon's ring inherits currentColor and its center node is
 * the brand accent, so the mark stays on-brand in both themes.
 */
export function AssistantAvatar({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "flex size-6 shrink-0 items-center justify-center rounded-full bg-brand/10 text-foreground/80",
        className,
      )}
      aria-hidden="true"
    >
      <AgoraIcon noSpin className="size-3.5" />
    </div>
  );
}
