import { describe, expect, it } from "vitest";
import { parseProtocolFallback } from "./protocol-fallback";

const raw = `request.PROGRESS: Checking prior notes and pinned state.
\`\`\`todo
- [~] Decide the implementation parts and owners
- [ ] Dispatch the required work to the squad
- [ ] Record the routing decision
\`\`\`The repository guardrails confirm this stays read-only.PROGRESS: Recording the routing decision.
\`\`\`todo
- [x] Decide the implementation parts and owners - [x] Dispatch the required work to the squad - [~] Record the routing decision
\`\`\`Dispatched the issue as a serial squad plan. No code changes were made.`;

describe("parseProtocolFallback", () => {
  it("extracts the latest activity, checklist, and final narrative", () => {
    expect(parseProtocolFallback(raw)).toMatchObject({
      progress: "Recording the routing decision.",
      todo: [
        { status: "done", text: "Decide the implementation parts and owners" },
        { status: "done", text: "Dispatch the required work to the squad" },
        { status: "active", text: "Record the routing decision" },
      ],
      final: "Dispatched the issue as a serial squad plan. No code changes were made.",
    });
  });

  it("leaves ordinary assistant Markdown alone", () => {
    expect(parseProtocolFallback("Progress: complete. Here is the result.")).toBeNull();
    expect(parseProtocolFallback("```todo\n- [ ] item\n```" )).toBeNull();
  });
});
