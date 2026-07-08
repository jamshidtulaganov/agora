/* eslint-disable i18next/no-literal-string */
"use client";

import { useState } from "react";
import { Send, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { Button } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@agora/ui/components/ui/dialog";

// BitrixSummaryAction lets a HUMAN review an agent's final summary and post it as
// a comment onto the linked Bitrix task. Renders nothing for issues that didn't
// come from Bitrix. The textarea is prefilled from the agent's final summary (the
// UI's best guess — branch name + bug causes usually live in the last agent
// comment); the human edits and confirms. The backend route is human-only, so
// nothing reaches Bitrix without this explicit action. Untranslated copy matches
// the rest of the bitrix slice.

function metaStr(
  meta: Record<string, unknown> | null | undefined,
  key: string,
): string {
  const v = meta?.[key];
  return typeof v === "string" ? v.trim() : "";
}

export function BitrixSummaryAction({
  issueId,
  metadata,
  prefill,
}: {
  issueId: string;
  metadata?: Record<string, unknown> | null;
  /** Best-effort prefill (the agent's final summary + branch). */
  prefill?: string;
}) {
  const linked = metaStr(metadata, "bitrix_task_id") !== "";
  const alreadyPosted = metaStr(metadata, "bitrix_summary_pushed") !== "";

  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const [posting, setPosting] = useState(false);

  if (!linked) return null;

  const openDialog = () => {
    setText((prefill ?? "").trim());
    setOpen(true);
  };

  const submit = async () => {
    const body = text.trim();
    if (!body) return;
    setPosting(true);
    try {
      await api.postBitrixSummary(issueId, body);
      toast.success("Summary posted to Bitrix");
      setOpen(false);
    } catch {
      toast.error("Failed to post the summary to Bitrix");
    } finally {
      setPosting(false);
    }
  };

  return (
    <>
      <button
        type="button"
        onClick={openDialog}
        title={
          alreadyPosted
            ? "Post an updated final summary to Bitrix"
            : "Review the agent's final summary and post it to Bitrix"
        }
        className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
      >
        <Send className="size-3 shrink-0" />
        <span className="truncate">
          {alreadyPosted ? "Re-post summary to Bitrix" : "Post summary to Bitrix"}
        </span>
      </button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Post final summary to Bitrix</DialogTitle>
          </DialogHeader>
          <p className="text-xs text-muted-foreground">
            Review the summary (branch name, bug causes) before it posts as a
            comment on the linked Bitrix task. Nothing is sent until you post.
          </p>
          <Textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={8}
            placeholder="Branch, bug cause, what changed…"
            className="font-mono text-xs"
          />
          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)} disabled={posting}>
              Cancel
            </Button>
            <Button onClick={submit} disabled={posting || !text.trim()}>
              {posting ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <Send className="size-3.5" />
              )}
              Post to Bitrix
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
