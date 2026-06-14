"use client";

import { DashboardLayout } from "@tandem/views/layout";
import { TandemIcon } from "@tandem/ui/components/common/tandem-icon";
import { SearchCommand, SearchTrigger } from "@tandem/views/search";
import { ChatFab, ChatWindow } from "@tandem/views/chat";
import { WebNotificationBridge } from "@/components/web-notification-bridge";

export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <DashboardLayout
      loadingIndicator={<TandemIcon className="size-6" />}
      searchSlot={<SearchTrigger />}
      extra={
        <>
          <SearchCommand />
          <ChatWindow />
          <ChatFab />
          <WebNotificationBridge />
        </>
      }
    >
      {children}
    </DashboardLayout>
  );
}
