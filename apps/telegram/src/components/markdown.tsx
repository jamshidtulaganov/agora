import ReactMarkdown, { defaultUrlTransform, type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "../lib/cn";

// Lightweight GFM markdown renderer for the Mini App. Deliberately NO katex /
// mermaid / syntax-highlight pipelines (they bloat the bundle 10×) — just GFM
// (tables, task lists, strikethrough, autolinks) styled for the dark Telegram
// theme. Agora mention tokens `[@Name](mention://type/id)` render as brand chips.

// Keep mention:// links (stripped by the default sanitizer otherwise); run every
// other URL through the default transform so javascript: and friends are dropped.
function urlTransform(url: string): string {
  return url.startsWith("mention://") ? url : defaultUrlTransform(url);
}

const components: Components = {
  a({ href, children, ...props }) {
    if (href?.startsWith("mention://")) {
      return <span className="font-medium text-brand">{children}</span>;
    }
    return (
      <a
        href={href}
        target="_blank"
        rel="noreferrer noopener"
        className="text-brand underline underline-offset-2"
        {...props}
      >
        {children}
      </a>
    );
  },
  p: ({ children }) => <p className="leading-relaxed">{children}</p>,
  h1: ({ children }) => <h1 className="mt-3 text-lg font-semibold first:mt-0">{children}</h1>,
  h2: ({ children }) => <h2 className="mt-3 text-base font-semibold first:mt-0">{children}</h2>,
  h3: ({ children }) => <h3 className="mt-2 text-[15px] font-semibold first:mt-0">{children}</h3>,
  ul: ({ children }) => <ul className="list-disc space-y-1 pl-5">{children}</ul>,
  ol: ({ children }) => <ol className="list-decimal space-y-1 pl-5">{children}</ol>,
  li: ({ children }) => <li className="leading-relaxed">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="border-l-2 border-border pl-3 text-muted-foreground">{children}</blockquote>
  ),
  hr: () => <hr className="border-border" />,
  strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
  // Fenced blocks carry a language-* class (they sit inside <pre>); render those
  // plainly and let <pre> own the scroll box. Bare inline code gets a chip.
  code: ({ className, children }) => {
    const isBlock = /\blanguage-/.test(className ?? "");
    if (isBlock) return <code className="font-mono text-[13px]">{children}</code>;
    return (
      <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[13px]">{children}</code>
    );
  },
  pre: ({ children }) => (
    <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-[13px] leading-relaxed">
      {children}
    </pre>
  ),
  table: ({ children }) => (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[13px]">{children}</table>
    </div>
  ),
  th: ({ children }) => (
    <th className="border border-border px-2 py-1 text-left font-semibold">{children}</th>
  ),
  td: ({ children }) => <td className="border border-border px-2 py-1">{children}</td>,
};

export function Markdown({ content, className }: { content: string; className?: string }) {
  return (
    <div className={cn("space-y-2 break-words text-[15px] text-foreground", className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} urlTransform={urlTransform} components={components}>
        {content}
      </ReactMarkdown>
    </div>
  );
}
