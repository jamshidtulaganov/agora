import { useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { Avatar } from "./avatar";

// A textarea with an "@" autocomplete that inserts Agora mention tokens
// `[@Name](mention://<type>/<id>)`. Mentioning a member notifies them; mentioning
// an agent triggers it server-side (once a runtime exists). The token is what the
// backend's util.ParseMentions reads; renderMentions() displays it as @Name.

type Candidate = { type: "member" | "agent"; id: string; name: string; isAgent: boolean };

// Active "@query" = an @ at start-or-after-whitespace, then non-space/@ chars,
// at the caret. Avoids triggering on emails ("a@b") and stops at spaces.
const TRIGGER_RE = /(?:^|\s)@([^\s@]*)$/;

export function MentionComposer({
  value,
  onChange,
  onSubmit,
  members,
  agents,
  placeholder,
  disabled,
}: {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  members: { user_id: string; name: string }[];
  agents: { id: string; name: string; archived_at?: string | null }[];
  placeholder?: string;
  disabled?: boolean;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const [query, setQuery] = useState<string | null>(null);
  const [anchor, setAnchor] = useState(0); // index of the triggering "@"

  // Auto-grow with the content (up to max-h-32) so multi-line drafts and
  // mention picking stay fully visible instead of scrolling inside a
  // one-line pill. Runs on every value change, including programmatic ones
  // (mention insertion, draft restore, clear-on-send).
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 128)}px`;
  }, [value]);

  const candidates: Candidate[] = [
    ...members.map((m) => ({ type: "member" as const, id: m.user_id, name: m.name, isAgent: false })),
    ...agents
      .filter((a) => !a.archived_at)
      .map((a) => ({ type: "agent" as const, id: a.id, name: a.name, isAgent: true })),
  ];

  const matches =
    query !== null
      ? candidates
          .filter((c) => c.name.toLowerCase().includes(query.toLowerCase()))
          .slice(0, 6)
      : [];
  const open = query !== null && matches.length > 0;

  const recompute = (text: string, caret: number) => {
    const before = text.slice(0, caret);
    const m = TRIGGER_RE.exec(before);
    if (m) {
      setQuery(m[1] ?? "");
      setAnchor(caret - (m[1]?.length ?? 0) - 1);
    } else {
      setQuery(null);
    }
  };

  const pick = (c: Candidate) => {
    const token = `[@${c.name}](mention://${c.type}/${c.id})`;
    const caret = anchor + 1 + (query?.length ?? 0); // end of the "@query"
    const next = value.slice(0, anchor) + token + " " + value.slice(caret);
    onChange(next);
    setQuery(null);
    const pos = anchor + token.length + 1;
    requestAnimationFrame(() => {
      const el = ref.current;
      if (el) {
        el.focus();
        el.setSelectionRange(pos, pos);
      }
    });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (open && e.key === "Enter") {
      e.preventDefault();
      pick(matches[0]!);
      return;
    }
    if (open && e.key === "Escape") {
      setQuery(null);
      return;
    }
    if (!open && e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      onSubmit();
    }
  };

  return (
    <div className="relative flex-1">
      {open && (
        <div className="absolute bottom-full left-0 right-0 mb-1.5 max-h-52 overflow-y-auto rounded-xl border border-border bg-popover shadow-lg ring-1 ring-foreground/5">
          {matches.map((c) => (
            <button
              key={c.type + c.id}
              type="button"
              // pointer-down (not click) so the textarea doesn't blur first on mobile
              onPointerDown={(e) => {
                e.preventDefault();
                pick(c);
              }}
              className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm transition-colors active:bg-accent"
            >
              <Avatar name={c.name} isAgent={c.isAgent} size={24} />
              <span className="truncate">{c.name}</span>
            </button>
          ))}
        </div>
      )}
      <textarea
        ref={ref}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          recompute(e.target.value, e.target.selectionStart ?? e.target.value.length);
        }}
        onKeyUp={(e) => {
          // keep the query in sync when the caret moves without changing text
          const el = e.currentTarget;
          recompute(el.value, el.selectionStart ?? el.value.length);
        }}
        onBlur={() => setQuery(null)}
        onKeyDown={onKeyDown}
        rows={1}
        placeholder={placeholder}
        disabled={disabled}
        className="max-h-32 min-h-[44px] w-full resize-none rounded-2xl bg-muted px-4 py-[11px] text-[15px] leading-[1.4] text-foreground outline-none placeholder:truncate placeholder:text-muted-foreground"
      />
    </div>
  );
}
