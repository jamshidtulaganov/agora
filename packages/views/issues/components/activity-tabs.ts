import type { TimelineEntry } from "@agora/core/types";

// Activity-feed tab: split the mixed issue timeline so Bitrix comments, agent
// responses, and in-Agora people discussion each have their own view. "all"
// keeps the unified feed.
export type ActivityTab = "all" | "agents" | "bitrix" | "people";

// Classify a timeline entry by the ORIGIN of its thread ROOT, so a whole thread
// stays together under one tab (an agent reply on a Bitrix comment lands in
// "bitrix", not "agents"). Precedence: a Bitrix marker (bitrix_comment_id) on
// the root wins, then an agent actor, else people/system. Activities have no
// parent, so they classify by their own actor.
export function classifyEntryRoot(
  entry: TimelineEntry,
  byId: Map<string, TimelineEntry>,
): Exclude<ActivityTab, "all"> {
  let cur: TimelineEntry | undefined = entry;
  // Walk to the thread root. Guard against a cycle (a corrupt parent_id chain)
  // with a visited set so this can never spin.
  const seen = new Set<string>();
  while (cur?.parent_id && byId.get(cur.parent_id) && !seen.has(cur.id)) {
    seen.add(cur.id);
    cur = byId.get(cur.parent_id);
  }
  const root = cur ?? entry;
  if (root.type === "comment" && root.bitrix_comment_id) return "bitrix";
  if (root.actor_type === "agent") return "agents";
  return "people";
}

// Per-tab top-level counts for the tab badges. Replies are NOT counted — they
// ride with their thread root. Computed off the full timeline so the badges stay
// stable regardless of the active tab.
export function activityTabCounts(timeline: TimelineEntry[]): {
  all: number;
  agents: number;
  bitrix: number;
  people: number;
} {
  const byId = new Map(timeline.map((e) => [e.id, e]));
  let agents = 0;
  let bitrix = 0;
  let people = 0;
  for (const e of timeline) {
    if (e.type === "comment" && e.parent_id) continue; // replies ride the root
    const c = classifyEntryRoot(e, byId);
    if (c === "agents") agents++;
    else if (c === "bitrix") bitrix++;
    else people++;
  }
  return { all: agents + bitrix + people, agents, bitrix, people };
}
