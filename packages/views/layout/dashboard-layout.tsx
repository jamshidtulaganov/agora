"use client";

import type { CSSProperties, ReactNode } from "react";
import { SidebarProvider, SidebarInset } from "@agora/ui/components/ui/sidebar";
import { cn } from "@agora/ui/lib/utils";
import { useAssistantPanelStore } from "@agora/core/assistant";
import { ModalRegistry } from "../modals/registry";
import { AppSidebar } from "./app-sidebar";
import { DashboardGuard } from "./dashboard-guard";
import { NavigationProgress } from "./navigation-progress";
import { WorkspacePresencePrefetch } from "./workspace-presence-prefetch";
import { NotificationToastBridge } from "./notification-toast-bridge";

interface DashboardLayoutProps {
  children: ReactNode;
  /** Rendered inside SidebarInset (e.g. AssistantPanel, AssistantFab — absolute-positioned overlays) */
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
  const isPanelOpen = useAssistantPanelStore((state) => state.isOpen);
  const isPanelExpanded = useAssistantPanelStore((state) => state.isExpanded);
  const panelWidth = useAssistantPanelStore((state) => state.panelWidth);
  const dockPanel = isPanelOpen && !isPanelExpanded;
  const panelDockStyle = {
    // Keep enough room for the normal floating panel without letting a very
    // large user-resized panel collapse the application below a useful width.
    "--assistant-reserved-width": `min(${panelWidth + 16}px, 42vw)`,
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
            dockPanel && "xl:pr-[var(--assistant-reserved-width)]",
          )}
          style={panelDockStyle}
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
