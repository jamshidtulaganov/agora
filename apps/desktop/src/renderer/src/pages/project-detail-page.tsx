import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ProjectDetail } from "@agora/views/projects/components";
import { useWorkspaceId } from "@agora/core/hooks";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>();
  const wsId = useWorkspaceId();
  const { data: project } = useQuery(projectDetailOptions(wsId, id!));

  useDocumentTitle(project ? `${project.icon || "📁"} ${project.title}` : "Project");

  if (!id) return null;
  return <ProjectDetail projectId={id} />;
}
