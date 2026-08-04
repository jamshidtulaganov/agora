import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { InboxItem } from "@agora/core/types";
import enInbox from "../locales/en/inbox.json";
import { NotificationCenter } from "./notification-center";

const { markAll, push } = vi.hoisted(() => ({
  markAll: vi.fn(),
  push: vi.fn(),
}));

vi.mock("@agora/core/inbox/mutations", () => ({
  useMarkAllInboxRead: () => ({ mutate: markAll, isPending: false }),
}));

vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ inbox: () => "/acme/inbox" }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push }),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (selector: (dictionary: typeof enInbox) => string) => selector(enInbox),
  }),
}));

vi.mock("@agora/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
  }) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
}));

function item(index: number, read = false): InboxItem {
  return {
    id: `item-${index}`,
    workspace_id: "ws-1",
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: "system",
    actor_id: null,
    type: "mentioned",
    severity: "info",
    issue_id: `issue-${index}`,
    title: `Notification ${index}`,
    body: `Body ${index}`,
    issue_status: null,
    read,
    archived: false,
    created_at: new Date(2026, 7, index).toISOString(),
    details: null,
  };
}

describe("NotificationCenter", () => {
  beforeEach(() => {
    markAll.mockReset();
    push.mockReset();
  });

  it("shows the total unread count while limiting the preview list", () => {
    render(<NotificationCenter items={Array.from({ length: 7 }, (_, i) => item(i + 1))} />);

    expect(screen.getByText("7")).toBeInTheDocument();
    expect(screen.getByText("Notification 7")).toBeInTheDocument();
    expect(screen.queryByText("Notification 1")).not.toBeInTheDocument();
  });

  it("opens a notification in the workspace inbox", async () => {
    render(<NotificationCenter items={[item(1)]} />);

    await userEvent.click(screen.getByText("Notification 1"));
    expect(push).toHaveBeenCalledWith("/acme/inbox?issue=issue-1");
  });

  it("marks every notification as read", async () => {
    render(<NotificationCenter items={[item(1)]} />);

    await userEvent.click(screen.getByText(enInbox.menu.mark_all_read));
    expect(markAll).toHaveBeenCalledWith(undefined, expect.any(Object));
  });
});
