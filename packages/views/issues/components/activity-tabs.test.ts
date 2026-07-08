import { describe, expect, it } from "vitest";
import type { TimelineEntry } from "@agora/core/types";
import { activityTabCounts, classifyEntryRoot } from "./activity-tabs";

function comment(
  id: string,
  overrides: Partial<TimelineEntry> = {},
): TimelineEntry {
  return {
    type: "comment",
    id,
    actor_type: "member",
    actor_id: "u1",
    created_at: "2026-07-08T10:00:00Z",
    ...overrides,
  };
}

function activity(id: string, actorType: string): TimelineEntry {
  return {
    type: "activity",
    id,
    actor_type: actorType,
    actor_id: "a1",
    action: "task_completed",
    created_at: "2026-07-08T10:00:00Z",
  };
}

function byId(entries: TimelineEntry[]) {
  return new Map(entries.map((e) => [e.id, e]));
}

describe("classifyEntryRoot", () => {
  it("routes a Bitrix-imported comment to 'bitrix'", () => {
    const e = comment("c1", { bitrix_comment_id: "999" });
    expect(classifyEntryRoot(e, byId([e]))).toBe("bitrix");
  });

  it("routes an agent comment to 'agents'", () => {
    const e = comment("c1", { actor_type: "agent" });
    expect(classifyEntryRoot(e, byId([e]))).toBe("agents");
  });

  it("routes a human/system comment to 'people'", () => {
    expect(classifyEntryRoot(comment("c1"), byId([comment("c1")]))).toBe(
      "people",
    );
    const sys = comment("c2", { actor_type: "system" });
    expect(classifyEntryRoot(sys, byId([sys]))).toBe("people");
  });

  it("classifies an activity by its own actor", () => {
    const all = [activity("a1", "agent"), activity("a2", "member")];
    const m = byId(all);
    expect(classifyEntryRoot(all[0]!, m)).toBe("agents");
    expect(classifyEntryRoot(all[1]!, m)).toBe("people");
  });

  it("a reply inherits its thread ROOT's tab — agent reply on a Bitrix comment is 'bitrix'", () => {
    const root = comment("root", { bitrix_comment_id: "999" });
    const reply = comment("r1", { actor_type: "agent", parent_id: "root" });
    const m = byId([root, reply]);
    expect(classifyEntryRoot(reply, m)).toBe("bitrix");
  });

  it("a deep reply walks up multiple levels to the root", () => {
    const root = comment("root", { actor_type: "agent" });
    const mid = comment("mid", { actor_type: "member", parent_id: "root" });
    const leaf = comment("leaf", { actor_type: "member", parent_id: "mid" });
    const m = byId([root, mid, leaf]);
    expect(classifyEntryRoot(leaf, m)).toBe("agents");
  });

  it("does not spin on a corrupt self-referential parent chain", () => {
    const a = comment("a", { parent_id: "b" });
    const b = comment("b", { parent_id: "a" });
    // Must terminate (visited guard) rather than loop forever.
    expect(classifyEntryRoot(a, byId([a, b]))).toBe("people");
  });
});

describe("activityTabCounts", () => {
  it("counts top-level entries per tab and excludes replies", () => {
    const timeline: TimelineEntry[] = [
      comment("b1", { bitrix_comment_id: "1" }),
      comment("b2", { bitrix_comment_id: "2" }),
      comment("ag1", { actor_type: "agent" }),
      comment("h1"),
      comment("reply", { actor_type: "agent", parent_id: "b1" }), // reply → not counted
      activity("act", "agent"),
    ];
    const counts = activityTabCounts(timeline);
    expect(counts).toEqual({ all: 5, agents: 2, bitrix: 2, people: 1 });
  });

  it("is empty for an empty timeline", () => {
    expect(activityTabCounts([])).toEqual({
      all: 0,
      agents: 0,
      bitrix: 0,
      people: 0,
    });
  });
});
