import { ThemeToggle } from "@agora/ui/components/common/theme-toggle";

// Shared chrome for the pre-dashboard auth flows (login, onboarding,
// invitations, create-workspace, invite). These render with semantic tokens,
// so the theme toggle flips them cleanly. Fixed top-right, above page content.
export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <>
      {children}
      <div className="fixed right-4 top-4 z-50">
        <ThemeToggle />
      </div>
    </>
  );
}
