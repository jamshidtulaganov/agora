import { describe, expect, it } from "vitest";

import { figmaRefsFrom } from "./links";

// Mirrors server/internal/figma/links_test.go — keep the two matrices in sync.
describe("figmaRefsFrom", () => {
  const mul348 =
    "https://www.figma.com/design/cF4PFq3P5NOyZvp01JSHnE/Sales-Doctor-Dashboard?node-id=208-5147&p=f&t=5N85gGmuiIY1odti-0";

  it("returns [] for text without figma links", () => {
    expect(figmaRefsFrom("")).toEqual([]);
    expect(figmaRefsFrom("see https://example.com/design/abc")).toEqual([]);
  });

  it("extracts the real MUL-348 design URL", () => {
    expect(figmaRefsFrom(`Создание интерфейсных компонентов. ${mul348}`)).toEqual([
      { url: mul348, file_key: "cF4PFq3P5NOyZvp01JSHnE", node_id: "208:5147" },
    ]);
  });

  it("handles file URLs without node-id", () => {
    expect(figmaRefsFrom("https://www.figma.com/file/AbCdEf123456/My-File")).toEqual([
      { url: "https://www.figma.com/file/AbCdEf123456/My-File", file_key: "AbCdEf123456", node_id: "" },
    ]);
  });

  it("handles proto URLs without www and normalizes node-id", () => {
    expect(figmaRefsFrom("https://figma.com/proto/AbCdEf123456/Flow?node-id=1-2")).toEqual([
      { url: "https://figma.com/proto/AbCdEf123456/Flow?node-id=1-2", file_key: "AbCdEf123456", node_id: "1:2" },
    ]);
  });

  it("decodes %3A-encoded node ids", () => {
    const url = "https://www.figma.com/design/AbCdEf123456/X?node-id=208%3A5147";
    expect(figmaRefsFrom(url)).toEqual([{ url, file_key: "AbCdEf123456", node_id: "208:5147" }]);
  });

  it("stops at markdown link delimiters", () => {
    const refs = figmaRefsFrom(
      "see [the design](https://www.figma.com/design/AbCdEf123456/X?node-id=3-4) for details",
    );
    expect(refs).toHaveLength(1);
    expect(refs[0]?.url.endsWith(")")).toBe(false);
    expect(refs[0]?.node_id).toBe("3:4");
  });

  it("keeps distinct nodes of one file, dedupes identical (file, node) pairs", () => {
    const refs = figmaRefsFrom(
      "https://www.figma.com/design/AbCdEf123456/X?node-id=1-1 " +
        "https://www.figma.com/design/AbCdEf123456/X?node-id=2-2 " +
        "https://www.figma.com/design/AbCdEf123456/Y?node-id=1-1",
    );
    expect(refs.map((r) => r.node_id)).toEqual(["1:1", "2:2"]);
  });

  it("rejects short file keys and non-design paths", () => {
    expect(figmaRefsFrom("https://www.figma.com/design/short/X?node-id=1-1")).toEqual([]);
    expect(figmaRefsFrom("https://www.figma.com/board/AbCdEf123456/Whiteboard")).toEqual([]);
  });
});
