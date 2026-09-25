"use client";

import { useCallback } from "react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useFileUpload } from "@agora/core/hooks/use-file-upload";
import { checkKnowledgeFile, useCreateKnowledgeDoc, type KnowledgeFileProblem } from "@agora/core/knowledge";
import { useT } from "../../i18n";

/**
 * Adds files to the knowledge base: each one is checked against the reader's
 * limits, uploaded with the shared upload helper, then registered with
 * POST /api/knowledge. One toast per file follows it from "Adding…" to added
 * or failed, and a failed file never stops the ones after it.
 */
export function useKnowledgeUpload(wsId: string) {
  const { t } = useT("knowledge");
  const { upload } = useFileUpload(api);
  const create = useCreateKnowledgeDoc(wsId);
  const { mutateAsync } = create;

  const problemText = useCallback(
    (problem: KnowledgeFileProblem): string => {
      switch (problem) {
        case "too_large":
          return t(($) => $.upload.too_large);
        case "empty":
          return t(($) => $.upload.empty);
        case "unsupported_type":
        default:
          return t(($) => $.upload.unsupported_type);
      }
    },
    [t],
  );

  const addFiles = useCallback(
    async (files: File[]) => {
      // One at a time: a department dropping twenty SOPs shouldn't open twenty
      // uploads at once, and the toasts then read top to bottom in order.
      for (const file of files) {
        const name = file.name;
        const problem = checkKnowledgeFile(file);
        if (problem) {
          toast.error(t(($) => $.upload.failed, { name }), { description: problemText(problem) });
          continue;
        }
        const toastId = toast.loading(t(($) => $.upload.adding, { name }));
        try {
          const uploaded = await upload(file);
          if (!uploaded?.id) throw new Error(t(($) => $.upload.failed, { name }));
          await mutateAsync({ attachment_id: uploaded.id });
          toast.success(t(($) => $.upload.added, { name }), {
            id: toastId,
            description: t(($) => $.upload.added_description),
          });
        } catch (err) {
          // 413 / 415 carry the server's reason; show it as-is.
          const reason = err instanceof Error && err.message ? err.message : undefined;
          toast.error(t(($) => $.upload.failed, { name }), { id: toastId, description: reason });
        }
      }
    },
    [mutateAsync, problemText, t, upload],
  );

  return { addFiles };
}
