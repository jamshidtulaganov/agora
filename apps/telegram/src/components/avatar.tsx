import { ActorAvatar } from "@agora/ui/components/common/actor-avatar";

// Agora's actor avatar, props-driven. Members render initials on a muted tile;
// agents render the Bot glyph — identical to web/desktop. Used for assignees,
// comment authors and chat agents so the Mini App reads as Agora.

function initialsOf(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0]! + parts[parts.length - 1]![0]!).toUpperCase();
}

export function Avatar({
  name,
  isAgent,
  avatarUrl,
  size = 28,
  className,
}: {
  name: string;
  isAgent?: boolean;
  avatarUrl?: string | null;
  size?: number;
  className?: string;
}) {
  return (
    <ActorAvatar
      name={name}
      initials={initialsOf(name)}
      avatarUrl={avatarUrl}
      isAgent={isAgent}
      size={size}
      className={className}
    />
  );
}
