import { describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AutomationCatalog } from "@agora/core/automations";
import { renderWithI18n } from "../../test/i18n";
import { AutomationFlowEditor, type AutomationFlowValue } from "./automation-flow-editor";

// A catalog shaped like the server's, plus one trigger/step the client has never
// heard of — the editor must render those too rather than dropping them.
const CATALOG: AutomationCatalog = {
  triggers: [
    { type: "tracker.stage_changed", fields: ["stage", "prev_stage", "status"] },
    { type: "issue.label_attached", fields: ["label", "status"] },
  ],
  steps: ["filter", "set_status", "send_telegram", "dispatch_slice_action"],
  operators: ["eq", "in", "exists", "has_label"],
  slice_action_kinds: ["run_review", "run_qa"],
  statuses: ["todo", "in_review", "done"],
  assign_targets: ["orchestrator", "agent", "none"],
  agent_selectors: ["", "reviewer", "qa", "agent"],
  telegram_targets: ["group", "owner"],
  template_variables: ["{{issue}}"],
  min_interval_default: 30,
  max_per_hour_default: 20,
};

function setup(value: AutomationFlowValue) {
  const onChange = vi.fn();
  const result = renderWithI18n(
    <AutomationFlowEditor value={value} catalog={CATALOG} onChange={onChange} />,
  );
  return { onChange, ...result };
}

describe("AutomationFlowEditor", () => {
  it("renders the trigger node with its translated label", () => {
    setup({ trigger_type: "tracker.stage_changed", conditions: [], actions: [] });
    expect(screen.getByText("When")).toBeInTheDocument();
    expect(screen.getByLabelText("Trigger")).toHaveValue("tracker.stage_changed");
    // No conditions yet → say so, rather than showing an empty row.
    expect(screen.getByText("Runs on every event of this type.")).toBeInTheDocument();
  });

  it("seeds a new condition from the chosen trigger's own fields", async () => {
    const user = userEvent.setup();
    // A rule must not be writable against a fact this event never carries, so the
    // first offered field comes from the catalog entry for THIS trigger.
    const { onChange } = setup({ trigger_type: "issue.label_attached", conditions: [], actions: [] });
    await user.click(screen.getByRole("button", { name: "Add condition" }));
    const next = onChange.mock.calls[0]?.[0] as AutomationFlowValue;
    expect(next.conditions).toHaveLength(1);
    expect(["label", "status"]).toContain(next.conditions[0]?.field);
    expect(CATALOG.operators).toContain(next.conditions[0]?.op);
  });

  it("only offers the current trigger's fields in the condition picker", () => {
    setup({
      trigger_type: "issue.label_attached",
      conditions: [{ field: "label", op: "eq", value: "review:fail" }],
      actions: [],
    });
    const fieldSelect = screen.getByLabelText("Field");
    // "attached label" and "status" belong to this trigger; the stage fields do not.
    expect(within(fieldSelect).getByText("attached label")).toBeInTheDocument();
    expect(within(fieldSelect).queryByText("tracker column")).not.toBeInTheDocument();
  });

  it("adds a step through the connector and reports it upward", async () => {
    const user = userEvent.setup();
    const { onChange } = setup({ trigger_type: "tracker.stage_changed", conditions: [], actions: [] });
    await user.click(screen.getByRole("button", { name: "Add step" }));
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0]?.[0] as AutomationFlowValue;
    expect(next.actions).toHaveLength(1);
    expect(CATALOG.steps).toContain(next.actions[0]?.type);
  });

  it("clears conditions when the trigger changes, because they name other facts", async () => {
    const user = userEvent.setup();
    const { onChange } = setup({
      trigger_type: "tracker.stage_changed",
      conditions: [{ field: "stage", op: "eq", value: "Code Review" }],
      actions: [],
    });
    await user.selectOptions(screen.getByLabelText("Trigger"), "issue.label_attached");
    const next = onChange.mock.calls[0]?.[0] as AutomationFlowValue;
    expect(next.trigger_type).toBe("issue.label_attached");
    expect(next.conditions).toEqual([]);
  });

  it("opens a filter node's parameters with the stop-the-flow hint", async () => {
    // The panel edits the SELECTED node, so the hint appears after the node is
    // opened on the canvas — the trigger's panel is what shows by default.
    const user = userEvent.setup();
    setup({
      trigger_type: "tracker.stage_changed",
      conditions: [],
      actions: [{ type: "filter", conditions: [{ field: "status", op: "eq", value: "in_review" }] }],
    });
    await user.click(screen.getByRole("button", { name: /Only continue if/ }));
    expect(screen.getAllByText("The flow stops here when this does not hold.").length).toBeGreaterThan(0);
  });

  it("renders the config fields of a known step and none for an unknown one", async () => {
    const user = userEvent.setup();
    const { unmount } = setup({
      trigger_type: "tracker.stage_changed",
      conditions: [],
      actions: [{ type: "send_telegram", config: { destination: "group", text: "hi" } }],
    });
    await user.click(screen.getByRole("button", { name: /Send a Telegram message/ }));
    expect(screen.getByText("Send to")).toBeInTheDocument();
    expect(screen.getByDisplayValue("hi")).toBeInTheDocument();
    unmount();

    // An unknown step type must still render (selectable on its canvas node,
    // labelled in the panel) with no config fields, so an older client
    // round-trips a newer flow instead of rewriting it.
    setup({
      trigger_type: "tracker.stage_changed",
      conditions: [],
      actions: [{ type: "send_carrier_pigeon", config: { flock: "3" } }],
    });
    await user.click(screen.getByRole("button", { name: /send carrier pigeon/ }));
    const stepSelect = screen.getByLabelText("Step");
    expect(within(stepSelect).getByText("send carrier pigeon")).toBeInTheDocument();
    expect(screen.queryByText("Send to")).not.toBeInTheDocument();
  });

  it("hides the value box for an operator that takes no value", () => {
    setup({
      trigger_type: "tracker.stage_changed",
      conditions: [{ field: "stage", op: "exists" }],
      actions: [],
    });
    expect(screen.queryByLabelText("Value")).not.toBeInTheDocument();
  });

  it("states the loop guard so a rule author is not surprised by a skipped run", () => {
    setup({ trigger_type: "tracker.stage_changed", conditions: [], actions: [] });
    expect(
      screen.getByText("A flow applies to the same task at most once every 30s, and at most 20 times an hour."),
    ).toBeInTheDocument();
  });

  it("keeps a stored trigger the catalog no longer lists", () => {
    setup({ trigger_type: "issue.retired_trigger", conditions: [], actions: [] });
    expect(screen.getByLabelText("Trigger")).toHaveValue("issue.retired_trigger");
  });
});
