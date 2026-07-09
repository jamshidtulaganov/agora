"use client";

import { cn } from "@agora/ui/lib/utils";
import { SidebarTrigger, useSidebarSafe } from "@agora/ui/components/ui/sidebar";

// Visible on ALL viewports: desktop users collapse the app nav to give the
// working surface (issue cockpit, QA live browser, editor) the full width;
// the ⌘/Ctrl+B shortcut and the sidebar rail still work as before.
function AppSidebarTrigger() {
  const sidebar = useSidebarSafe();
  if (!sidebar) return null;
  return <SidebarTrigger className="mr-2" />;
}

interface PageHeaderProps {
  children: React.ReactNode;
  className?: string;
}

export function PageHeader({ children, className }: PageHeaderProps) {
  return (
    <div className={cn("flex h-12 shrink-0 items-center border-b px-4", className)}>
      <AppSidebarTrigger />
      {children}
    </div>
  );
}
