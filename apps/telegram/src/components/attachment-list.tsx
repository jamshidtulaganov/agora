import { FileText, Film, Paperclip } from "lucide-react";
import type { Attachment } from "@agora/core/types";
import { sameOriginFileUrl, isImage, isVideo, humanSize } from "../lib/file-url";

// Renders an attachment array (on a comment) as native previews: images as
// thumbnails, videos with controls, everything else (pdf, docs) as a tappable
// file chip. Agora-hosted URLs are forced same-origin so the Mini App proxy
// serves them from the private backend.
export function AttachmentList({ attachments }: { attachments: Attachment[] }) {
  if (!attachments || attachments.length === 0) return null;

  // download_url is the click-time URL (may be a short-lived signed link);
  // fall back to url / markdown_url for older server responses.
  const srcOf = (a: Attachment) =>
    sameOriginFileUrl(a.download_url || a.url || a.markdown_url);

  return (
    <div className="mt-2 flex flex-col gap-2">
      {attachments.map((a) => {
        const src = srcOf(a);
        if (isImage(a.content_type)) {
          return (
            <a key={a.id} href={src} target="_blank" rel="noreferrer noopener" className="block">
              <img
                src={src}
                alt={a.filename}
                loading="lazy"
                className="max-h-64 w-auto max-w-full rounded-lg border border-border object-contain"
              />
            </a>
          );
        }
        if (isVideo(a.content_type)) {
          return (
            <video
              key={a.id}
              src={src}
              controls
              preload="metadata"
              className="max-h-72 w-full rounded-lg border border-border"
            />
          );
        }
        const isPdf = a.content_type === "application/pdf";
        const Icon = isPdf ? FileText : a.content_type ? Paperclip : Film;
        return (
          <a
            key={a.id}
            href={src}
            target="_blank"
            rel="noreferrer noopener"
            className="flex items-center gap-2.5 rounded-lg border border-border bg-muted px-3 py-2 active:bg-accent"
          >
            <Icon className="size-5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium text-foreground">
                {a.filename}
              </span>
              <span className="block text-[11px] text-muted-foreground">
                {humanSize(a.size_bytes)}
              </span>
            </span>
          </a>
        );
      })}
    </div>
  );
}
