"use client";

import type { CSSProperties, ReactNode } from "react";
import { SidebarProvider, SidebarInset } from "@agora/ui/components/ui/sidebar";
import { cn } from "@agora/ui/lib/utils";
import { useChatStore } from "@agora/core/chat";
import { ModalRegistry } from "../modals/registry";
import { AppSidebar } from "./app-sidebar";
import { DashboardGuard } from "./dashboard-guard";
import { NavigationProgress } from "./navigation-progress";
import { WorkspacePresencePrefetch } from "./workspace-presence-prefetch";
import { NotificationToastBridge } from "./notification-toast-bridge";

interface DashboardLayoutProps {
  children: ReactNode;
  /** Rendered inside SidebarInset (e.g. ChatWindow, ChatFab — absolute-positioned overlays) */
  extra?: ReactNode;
  /** Rendered inside sidebar header as a search trigger */
  searchSlot?: ReactNode;
  /** Loading indicator */
  loadingIndicator?: ReactNode;
}

export function DashboardLayout({
  children,
  extra,
  searchSlot,
  loadingIndicator,
}: DashboardLayoutProps) {
  const isChatOpen = useChatStore((state) => state.isOpen);
  const isChatExpanded = useChatStore((state) => state.isExpanded);
  const chatWidth = useChatStore((state) => state.chatWidth);
  const dockChat = isChatOpen && !isChatExpanded;
  const chatDockStyle = {
    // Keep enough room for the normal floating chat without letting a very
    // large user-resized panel collapse the application below a useful width.
    "--chat-reserved-width": `min(${chatWidth + 16}px, 42vw)`,
  } as CSSProperties;

  return (
    <DashboardGuard
      loadingFallback={
        <div className="flex h-svh items-center justify-center">
          {loadingIndicator}
        </div>
      }
    >
      <SidebarProvider className="h-svh">
        <WorkspacePresencePrefetch />
        <NotificationToastBridge />
        <AppSidebar searchSlot={searchSlot} />
        <SidebarInset
          className={cn(
            "relative overflow-hidden",
            dockChat && "xl:pr-[var(--chat-reserved-width)]",
          )}
          style={chatDockStyle}
        >
          <NavigationProgress />
          {children}
          <ModalRegistry />
          {extra}
        </SidebarInset>
      </SidebarProvider>
    </DashboardGuard>
  );
}
