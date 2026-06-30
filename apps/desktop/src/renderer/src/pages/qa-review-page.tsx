import { useParams } from "react-router-dom";
import { QAReviewPage as SharedQAReviewPage } from "@agora/views/qa";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function QAReviewPage() {
  const { id } = useParams<{ id: string }>();
  useDocumentTitle("QA review");
  if (!id) return null;
  return <SharedQAReviewPage issueId={id} />;
}
