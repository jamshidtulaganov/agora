import { useQuery } from "@tanstack/react-query";
import { getApi } from "@agora/core/api";

// Resolves the workspace a deep-linked issue belongs to from its UUID, so the
// Mini App opens it in the right workspace even when the link carried no slug
// (legacy links) or the user's last workspace differs. Falls back to the slug
// the link did carry, then null. The result feeds useActiveWorkspace's
// preferredSlug; it can resolve a render after launch (the shell heals).
export function useDeepLinkWorkspace(
  issueId: string | null | undefined,
  fallbackSlug: string | null | undefined,
): string | null {
  const { data } = useQuery({
    queryKey: ["tg-locate-issue", issueId],
    queryFn: () => getApi().locateIssue(issueId!),
    enabled: !!issueId,
    staleTime: Infinity,
  });
  return data?.workspace_slug ?? fallbackSlug ?? null;
}
