"use client";

import { use } from "react";
import { QAReviewPage } from "@agora/views/qa";

export default function Page({
  params,
}: {
  params: Promise<{ issueId: string }>;
}) {
  const { issueId } = use(params);
  return <QAReviewPage issueId={issueId} />;
}
