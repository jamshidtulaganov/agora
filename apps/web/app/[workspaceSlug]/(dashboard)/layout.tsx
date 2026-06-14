"use client";

import { DashboardLayout } from "@agora/views/layout";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { SearchCommand, SearchTrigger } from "@agora/views/search";
import { ChatFab, ChatWindow } from "@agora/views/chat";
import { WebNotificationBridge } from "@/components/web-notification-bridge";

export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <DashboardLayout
      loadingIndicator={<AgoraIcon className="size-6" />}
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
