"use client";

import { IssuesPage } from "@tandem/views/issues/components";
import { ErrorBoundary } from "@tandem/ui/components/common/error-boundary";

export default function Page() {
  return (
    <ErrorBoundary>
      <IssuesPage />
    </ErrorBoundary>
  );
}
