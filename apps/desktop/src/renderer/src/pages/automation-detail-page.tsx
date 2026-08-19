import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { AutomationDetailPage as AutomationDetail } from "@agora/views/automations/components";
import { useWorkspaceId } from "@agora/core/hooks";
import { automationDetailOptions } from "@agora/core/automations";
import { useDocumentTitle } from "@/hooks/use-document-title";

// Desktop param bridge: react-router owns the id, the shared view renders it. The
// detail query is read here only for the window title — "new" has no row yet, so the
// query stays disabled for it and the title falls back.
export function AutomationDetailPage() {
  const { id } = useParams<{ id: string }>();
  const wsId = useWorkspaceId();
  const { data } = useQuery(automationDetailOptions(wsId, id ?? "", { enabled: Boolean(id) && id !== "new" }));

  useDocumentTitle(data?.name ? `🔀 ${data.name}` : "Automation");

  if (!id) return null;
  return <AutomationDetail automationId={id} />;
}
