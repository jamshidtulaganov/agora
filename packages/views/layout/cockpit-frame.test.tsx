import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { CockpitFrame } from "./cockpit-frame";

// The desktop path renders a real `react-resizable-panels` Group/Panel,
// which measures real layout to drive collapse/expand — meaningless in
// jsdom (no layout engine) and prone to timing flakiness. issue-detail's own
// test suite mocks the same module for the same reason; mirror that
// established, low-risk pattern here. The mobile path (Sheet) stays real —
// it's a plain boolean-driven Base UI Dialog, no layout dependency.
vi.mock("react-resizable-panels", () => ({
  Group: ({ children, ...props }: any) => (
    <div data-testid="panel-group" {...props}>
      {children}
    </div>
  ),
  Panel: ({ children, ...props }: any) => (
    <div data-testid="panel" {...props}>
      {children}
    </div>
  ),
  Separator: ({ children, ...props }: any) => (
    <div data-testid="panel-handle" {...props}>
      {children}
    </div>
  ),
  useDefaultLayout: () => ({ defaultLayout: undefined, onLayoutChanged: vi.fn() }),
  usePanelRef: () => ({ current: { isCollapsed: () => false, expand: vi.fn(), collapse: vi.fn() } }),
}));

const mockViewport = vi.hoisted(() => ({ isMobile: false }));

vi.mock("@agora/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mockViewport.isMobile,
}));

describe("CockpitFrame", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
  });

  it("renders the header, content and rail slots on desktop", () => {
    render(
      <CockpitFrame layoutId="test-layout" header={<div>Header content</div>} rail={<div>Rail content</div>}>
        <div>Body content</div>
      </CockpitFrame>,
    );

    expect(screen.getByText("Header content")).toBeInTheDocument();
    expect(screen.getByText("Body content")).toBeInTheDocument();
    expect(screen.getByText("Rail content")).toBeInTheDocument();
  });

  it("renders nothing extra when topStrip is omitted, and renders it between header and body when provided", () => {
    const { rerender } = render(
      <CockpitFrame layoutId="test-layout" header={<div>Header</div>} rail={<div>Rail</div>}>
        <div>Body</div>
      </CockpitFrame>,
    );
    expect(screen.queryByText("Stepper strip")).not.toBeInTheDocument();

    rerender(
      <CockpitFrame
        layoutId="test-layout"
        header={<div>Header</div>}
        rail={<div>Rail</div>}
        topStrip={<div>Stepper strip</div>}
      >
        <div>Body</div>
      </CockpitFrame>,
    );

    expect(screen.getByText("Stepper strip")).toBeInTheDocument();
  });

  it("flips the rail open state via the toggle callback handed to a function header (mobile Sheet)", () => {
    mockViewport.isMobile = true;

    render(
      <CockpitFrame
        layoutId="test-layout"
        header={({ open, toggle }) => (
          <button type="button" onClick={toggle}>
            {open ? "rail open" : "rail closed"}
          </button>
        )}
        rail={<div>Rail content</div>}
      >
        <div>Body content</div>
      </CockpitFrame>,
    );

    // Closed by default on mobile — the Sheet hasn't mounted its content yet.
    expect(screen.getByRole("button", { name: "rail closed" })).toBeInTheDocument();
    expect(screen.queryByText("Rail content")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "rail closed" }));

    // Base UI's Dialog marks the background content inert while the Sheet is
    // open, which excludes it from the accessible tree — assert by text
    // (unaffected by aria-hidden) rather than role.
    expect(screen.getByText("rail open")).toBeInTheDocument();
    expect(screen.getByText("Rail content")).toBeInTheDocument();
  });

  it("does not render a resizable panel group on mobile", () => {
    mockViewport.isMobile = true;

    render(
      <CockpitFrame layoutId="test-layout" header={<div>Header</div>} rail={<div>Rail</div>}>
        <div>Body</div>
      </CockpitFrame>,
    );

    expect(screen.queryByTestId("panel-group")).not.toBeInTheDocument();
  });
});
