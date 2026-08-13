import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { BitrixProjectChip, BitrixTaskLink } from "./bitrix-task-link";

describe("BitrixProjectChip", () => {
  it("names the source Bitrix workgroup", () => {
    render(<BitrixProjectChip metadata={{ bitrix_group_name: "Iyun Sprint (8)" }} />);
    expect(screen.getByText("Iyun Sprint (8)")).toBeTruthy();
  });

  it("renders nothing for issues that never came from Bitrix", () => {
    const { container } = render(<BitrixProjectChip metadata={{}} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders nothing for a mirror imported before the group was stamped", () => {
    // Older mirrors carry bitrix_task_id but no group — the chip must stay absent
    // rather than showing an empty pill.
    const { container } = render(
      <BitrixProjectChip metadata={{ bitrix_task_id: "36251", bitrix_stage: "Code Review" }} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("ignores a non-string group name instead of throwing", () => {
    const { container } = render(<BitrixProjectChip metadata={{ bitrix_group_name: 153 }} />);
    expect(container.firstChild).toBeNull();
  });
});

describe("BitrixTaskLink", () => {
  it("leads with the kanban stage", () => {
    render(
      <BitrixTaskLink
        metadata={{ bitrix_task_url: "https://x/tasks/1", bitrix_stage: "Ready for release" }}
      />,
    );
    expect(screen.getByText("Ready for release")).toBeTruthy();
  });

  it("falls back to a generic label when no stage is known", () => {
    render(<BitrixTaskLink metadata={{ bitrix_task_url: "https://x/tasks/1" }} />);
    expect(screen.getByText("Open in Bitrix")).toBeTruthy();
  });

  it("marks a stage that maps to nothing", () => {
    const { container } = render(
      <BitrixTaskLink
        metadata={{
          bitrix_task_url: "https://x/tasks/1",
          bitrix_stage: "EPIC",
          bitrix_stage_mapped: "no",
        }}
      />,
    );
    // The warning icon is the only signal that the status came from coarse STATUS.
    expect(container.querySelector("svg.text-warning")).toBeTruthy();
  });

  it("does not warn when the flag is absent (older mirrors)", () => {
    const { container } = render(
      <BitrixTaskLink metadata={{ bitrix_task_url: "https://x/tasks/1", bitrix_stage: "Testing" }} />,
    );
    expect(container.querySelector("svg.text-warning")).toBeNull();
  });
});
