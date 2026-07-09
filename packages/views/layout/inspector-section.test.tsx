import { describe, it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { InspectorSection } from "./inspector-section";

describe("InspectorSection", () => {
  it("renders title and children when defaultOpen", () => {
    render(
      <InspectorSection title="Properties" defaultOpen>
        <div>Section body</div>
      </InspectorSection>,
    );

    expect(screen.getByText("Properties")).toBeInTheDocument();
    expect(screen.getByText("Section body")).toBeInTheDocument();
  });

  it("hides children by default (defaultOpen omitted)", () => {
    render(
      <InspectorSection title="Properties">
        <div>Section body</div>
      </InspectorSection>,
    );

    expect(screen.getByText("Properties")).toBeInTheDocument();
    expect(screen.queryByText("Section body")).not.toBeInTheDocument();
  });

  it("toggles children visibility when the header button is clicked", () => {
    render(
      <InspectorSection title="Properties" defaultOpen>
        <div>Section body</div>
      </InspectorSection>,
    );

    expect(screen.getByText("Section body")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Properties/ }));
    expect(screen.queryByText("Section body")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Properties/ }));
    expect(screen.getByText("Section body")).toBeInTheDocument();
  });

  it("renders an actions node in the toggle row", () => {
    render(
      <InspectorSection
        title="Properties"
        defaultOpen
        actions={<span data-testid="section-badge">3 open</span>}
      >
        <div>Section body</div>
      </InspectorSection>,
    );

    expect(screen.getByTestId("section-badge")).toBeInTheDocument();
    // The badge sits inside the same toggle button as the title.
    expect(screen.getByRole("button", { name: /Properties/ })).toContainElement(
      screen.getByTestId("section-badge"),
    );
  });
});
