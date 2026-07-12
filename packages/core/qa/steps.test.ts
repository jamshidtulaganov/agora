import { describe, it, expect } from "vitest";
import { parseSteps, serializeSteps, type ParsedStep } from "./steps";

describe("parseSteps / serializeSteps round trip", () => {
  it("round-trips a canonical multi-step case with and without expects", () => {
    const steps: ParsedStep[] = [
      { action: "Open the checkout page" },
      { action: "Add an item to the cart", expects: "Cart count increments to 1" },
      { action: "Submit payment", expects: "Order confirmation banner appears" },
    ];
    const text = serializeSteps(steps);
    expect(text).toBe(
      "1. Open the checkout page\n" +
        "2. Add an item to the cart → expects: Cart count increments to 1\n" +
        "3. Submit payment → expects: Order confirmation banner appears",
    );
    expect(parseSteps(text)).toEqual(steps);
  });

  it("degrades a legacy free-text blob into N action-only steps, never throwing", () => {
    const legacy = "1. add to cart\n2. pay\nconfirm the email arrived";
    expect(() => parseSteps(legacy)).not.toThrow();
    expect(parseSteps(legacy)).toEqual([
      { action: "add to cart" },
      { action: "pay" },
      { action: "confirm the email arrived" },
    ]);
  });

  it("drops only blank lines, never throws", () => {
    const withBlankLines = "1. step one\n\n\n2. step two\n   \n3. step three";
    expect(() => parseSteps(withBlankLines)).not.toThrow();
    expect(parseSteps(withBlankLines)).toHaveLength(3);
  });

  it("keeps an arrow embedded in the action text when not followed by 'expects:'", () => {
    const line = "1. Click the arrow → symbol button";
    expect(parseSteps(line)).toEqual([{ action: "Click the arrow → symbol button" }]);
  });

  it("splits on the LAST arrow-expects marker, keeping an earlier embedded arrow in the action", () => {
    const line = "1. Click Next → Confirm → expects: wizard advances to step 2";
    expect(parseSteps(line)).toEqual([
      { action: "Click Next → Confirm", expects: "wizard advances to step 2" },
    ]);
  });

  it("accepts the ASCII '->' fallback and is case-insensitive on 'expects'", () => {
    expect(parseSteps("1. Click save -> Expects: toast appears")).toEqual([
      { action: "Click save", expects: "toast appears" },
    ]);
  });

  it("preserves unicode content and round-trips it", () => {
    const line = "1. 输入中文字符 → expects: 显示成功提示 ✅";
    expect(parseSteps(line)).toEqual([{ action: "输入中文字符", expects: "显示成功提示 ✅" }]);
    expect(serializeSteps(parseSteps(line))).toBe(line);
  });

  it("round-trips 200 steps without loss", () => {
    const steps: ParsedStep[] = Array.from({ length: 200 }, (_, i) => ({
      action: `Do thing #${i}`,
      ...(i % 2 === 0 ? { expects: `Thing #${i} happens` } : {}),
    }));
    const text = serializeSteps(steps);
    const parsed = parseSteps(text);
    expect(parsed).toHaveLength(200);
    expect(parsed).toEqual(steps);
  });

  it("never throws and returns [] on empty or whitespace-only input", () => {
    expect(parseSteps("")).toEqual([]);
    expect(parseSteps("   \n  \n\t")).toEqual([]);
  });

  it("is idempotent: re-parsing a serialized round-trip is a fixed point", () => {
    const legacyBlobs = [
      "1) do a thing\n2) do another",
      "no numbering at all, just prose",
      "1. mixed → expects: yes\nnot numbered, no expects\n3. third → expects: third result",
      "→ expects: an orphaned expects-only line",
    ];
    for (const blob of legacyBlobs) {
      const once = parseSteps(blob);
      const twice = parseSteps(serializeSteps(once));
      expect(twice).toEqual(once);
    }
  });

  it("never loses the text of an orphaned expects-only line (empty action side)", () => {
    const parsed = parseSteps("→ expects: an orphaned expects-only line");
    expect(parsed).toEqual([{ action: "→ expects: an orphaned expects-only line" }]);
  });
});
