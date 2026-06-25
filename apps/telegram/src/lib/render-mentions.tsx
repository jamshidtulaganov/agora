import { Fragment, type ReactNode } from "react";

// Render comment/message content, turning Agora mention tokens
// `[@Label](mention://<type>/<id>)` into styled @Label chips and leaving the
// rest as plain text. Mirrors the backend's util.MentionRe so what we insert is
// exactly what renders. Newlines are preserved by the caller's whitespace-pre-wrap.
const MENTION_RE =
  /\[@?(.+?)\]\(mention:\/\/(?:member|agent|squad|issue|all)\/(?:[0-9a-fA-F-]+|all)\)/g;

export function renderMentions(content: string): ReactNode {
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  let m: RegExpExecArray | null;
  MENTION_RE.lastIndex = 0;
  while ((m = MENTION_RE.exec(content)) !== null) {
    if (m.index > last) {
      out.push(<Fragment key={key++}>{content.slice(last, m.index)}</Fragment>);
    }
    out.push(
      <span key={key++} className="font-medium text-brand">
        @{m[1]}
      </span>,
    );
    last = m.index + m[0].length;
  }
  if (last < content.length) {
    out.push(<Fragment key={key++}>{content.slice(last)}</Fragment>);
  }
  return out;
}
