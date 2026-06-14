"use client";

import { IssuesPage } from "@agora/views/issues/components";
import { ErrorBoundary } from "@agora/ui/components/common/error-boundary";

export default function Page() {
  return (
    <ErrorBoundary>
      <IssuesPage />
    </ErrorBoundary>
  );
}
