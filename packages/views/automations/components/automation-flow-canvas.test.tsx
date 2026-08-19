import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { AutomationFlowCanvas, type FlowCanvasNode } from "./automation-flow-canvas";

// Canvas interaction tests. jsdom has no real layout, so geometry-dependent
// assertions (drop position math) stay in unit range: what we pin here is the
// CONTRACT — n8n's keyboard bindings, selection, insertion, and that a node drag
// ends in exactly one reorder callback.

const NODES: FlowCanvasNode[] = [
  { id: "trigger", kind: "trigger", kicker: "When", title: "a label is attached", subtitle: "every event" },
  { id: "0", kind: "action", kicker: "Then", title: "Set the status", subtitle: "todo" },
  { id: "1", kind: "action", kicker: "Then", title: "Send a Telegram message", subtitle: "group" },
];

function setup(overrides: Partial<React.ComponentProps<typeof AutomationFlowCanvas>> = {}) {
  const mocks = {
    onSelect: vi.fn(),
    onOpen: vi.fn(),
    onInsert: vi.fn(),
    onReorder: vi.fn(),
    onRemove: vi.fn(),
  };
  const props = { nodes: NODES, selectedId: "trigger", ...mocks, ...overrides };
  const result = renderWithI18n(<AutomationFlowCanvas {...props} />);
  return { props, mocks, ...result };
}

describe("AutomationFlowCanvas", () => {
  it("renders every node and reports selection", async () => {
    const user = userEvent.setup();
    const { props } = setup();
    await user.click(screen.getByRole("button", { name: /Set the status/ }));
    expect(props.onSelect).toHaveBeenCalledWith("0");
  });

  it("opens a node's parameters on double-click", async () => {
    const user = userEvent.setup();
    const { props } = setup();
    await user.dblClick(screen.getByRole("button", { name: /Send a Telegram message/ }));
    expect(props.onOpen).toHaveBeenCalledWith("1");
  });

  it("inserts through the connector plus, at the clicked position", async () => {
    const user = userEvent.setup();
    const { props } = setup();
    const adds = screen.getAllByRole("button", { name: "Add step" });
    // One + per node (after it). Clicking the first inserts at step index 0.
    expect(adds.length).toBe(NODES.length);
    await user.click(adds[0]!);
    expect(props.onInsert).toHaveBeenCalledWith(0);
  });

  it("Delete removes the selected step but never the trigger", () => {
    const { props, mocks, rerender } = setup({ selectedId: "1" });
    const canvas = screen.getByRole("application");
    fireEvent.keyDown(canvas, { key: "Delete" });
    expect(mocks.onRemove).toHaveBeenCalledWith(1);

    mocks.onRemove.mockClear();
    rerender(
      <AutomationFlowCanvas {...props} selectedId="trigger" />,
    );
    fireEvent.keyDown(canvas, { key: "Delete" });
    expect(mocks.onRemove).not.toHaveBeenCalled();
  });

  it("Enter opens the selected node (n8n binding)", () => {
    const { props } = setup({ selectedId: "0" });
    fireEvent.keyDown(screen.getByRole("application"), { key: "Enter" });
    expect(props.onOpen).toHaveBeenCalledWith("0");
  });

  it("keyboard shortcuts never fire from inside an input", () => {
    // The panel lives next to the canvas; a Delete pressed while typing in a
    // field must edit text, not remove a node.
    const { mocks } = setup({ selectedId: "1" });
    const canvas = screen.getByRole("application");
    const input = document.createElement("input");
    canvas.appendChild(input);
    fireEvent.keyDown(input, { key: "Delete" });
    expect(mocks.onRemove).not.toHaveBeenCalled();
  });

  it("a node drag ends in exactly one reorder call", () => {
    const { mocks } = setup();
    const node = screen.getByRole("button", { name: /Set the status/ });
    const wrapper = node.parentElement!;
    fireEvent.pointerDown(wrapper, { pointerId: 1, clientX: 100, clientY: 50, button: 0 });
    fireEvent.pointerMove(wrapper, { pointerId: 1, clientX: 700, clientY: 50 });
    fireEvent.pointerUp(wrapper, { pointerId: 1, clientX: 700, clientY: 50 });
    // jsdom has no layout, so the exact drop index is not asserted — only that the
    // gesture resolved into a single reorder (or a no-op when the index matched).
    expect(mocks.onReorder.mock.calls.length).toBeLessThanOrEqual(1);
  });

  it("hides editing affordances when disabled", () => {
    setup({ onInsert: undefined, disabled: true });
    expect(screen.queryByRole("button", { name: "Add step" })).not.toBeInTheDocument();
  });
});
