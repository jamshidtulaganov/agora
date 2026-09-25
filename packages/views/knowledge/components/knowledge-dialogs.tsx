"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { useCreateKnowledgeDoc, type KnowledgeDoc } from "@agora/core/knowledge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@agora/ui/components/ui/alert-dialog";
import { Button } from "@agora/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { Input } from "@agora/ui/components/ui/input";
import { Label } from "@agora/ui/components/ui/label";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { useT } from "../../i18n";

/** A Markdown note written straight onto the knowledge base. */
export function NewNoteDialog({
  wsId,
  open,
  onOpenChange,
}: {
  wsId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("knowledge");
  const create = useCreateKnowledgeDoc(wsId);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");

  useEffect(() => {
    if (!open) {
      setTitle("");
      setBody("");
    }
  }, [open]);

  const canSave = title.trim() !== "" && body.trim() !== "" && !create.isPending;

  const save = async () => {
    if (!canSave) return;
    try {
      await create.mutateAsync({ title: title.trim(), body });
      toast.success(t(($) => $.note.added));
      onOpenChange(false);
    } catch (err) {
      toast.error(t(($) => $.note.failed), {
        description: err instanceof Error ? err.message : undefined,
      });
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(($) => $.note.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.note.description)}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <div className="space-y-1">
            <Label htmlFor="knowledge-note-title" className="text-xs text-muted-foreground">
              {t(($) => $.note.title_label)}
            </Label>
            <Input
              id="knowledge-note-title"
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder={t(($) => $.note.title_placeholder)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="knowledge-note-body" className="text-xs text-muted-foreground">
              {t(($) => $.note.body_label)}
            </Label>
            <Textarea
              id="knowledge-note-body"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault();
                  void save();
                }
              }}
              rows={10}
              className="max-h-[50vh] resize-y font-mono text-xs"
              placeholder={t(($) => $.note.body_placeholder)}
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t(($) => $.note.cancel)}
            </Button>
            <Button type="submit" disabled={!canSave}>
              {create.isPending ? t(($) => $.note.saving) : t(($) => $.note.save)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function RenameDocDialog({
  doc,
  onOpenChange,
  onRename,
}: {
  /** The document being renamed; null closes the dialog. */
  doc: KnowledgeDoc | null;
  onOpenChange: (open: boolean) => void;
  onRename: (doc: KnowledgeDoc, title: string) => void;
}) {
  const { t } = useT("knowledge");
  const [title, setTitle] = useState("");

  useEffect(() => {
    setTitle(doc?.title ?? "");
  }, [doc]);

  const trimmed = title.trim();
  const canSave = doc !== null && trimmed !== "" && trimmed !== doc.title;

  return (
    <Dialog open={doc !== null} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.documents.rename_title)}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (doc && canSave) onRename(doc, trimmed);
          }}
        >
          <div className="space-y-1">
            <Label htmlFor="knowledge-rename" className="text-xs text-muted-foreground">
              {t(($) => $.documents.rename_label)}
            </Label>
            <Input id="knowledge-rename" autoFocus value={title} onChange={(e) => setTitle(e.target.value)} />
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t(($) => $.documents.rename_cancel)}
            </Button>
            <Button type="submit" disabled={!canSave}>
              {t(($) => $.documents.rename_save)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function RemoveDocDialog({
  doc,
  onOpenChange,
  onConfirm,
}: {
  doc: KnowledgeDoc | null;
  onOpenChange: (open: boolean) => void;
  onConfirm: (doc: KnowledgeDoc) => void;
}) {
  const { t } = useT("knowledge");
  return (
    <AlertDialog open={doc !== null} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.documents.remove_title, { title: doc?.title ?? "" })}</AlertDialogTitle>
          <AlertDialogDescription>{t(($) => $.documents.remove_description)}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t(($) => $.documents.remove_cancel)}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => doc && onConfirm(doc)}>
            {t(($) => $.documents.remove_confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
