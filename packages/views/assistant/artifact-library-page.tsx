"use client";

import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Boxes, Plus, Search, ArrowUpRight } from "lucide-react";
import { assistantArtifactLibraryOptions, useAssistantStore, useCreateAssistantSession } from "@agora/core/assistant";
import { useWorkspacePaths } from "@agora/core/paths";
import { useNavigation } from "../navigation";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../i18n";
import { PageHeader } from "../layout/page-header";
import { ArtifactPane } from "./components/artifact-pane";
import { artifactKindIcon, useArtifactKindLabel } from "./components/artifact-card";

export function ArtifactLibraryPage() {
  const { t } = useT("layout");
  const kindLabel = useArtifactKindLabel();
  const query = useInfiniteQuery(assistantArtifactLibraryOptions());
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const setSession = useAssistantStore((s) => s.setActiveSession);
  const setArtifact = useAssistantStore((s) => s.setOpenArtifact);
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const createSession = useCreateAssistantSession();
  const artifacts = [...new Map((query.data?.pages.flat() ?? []).map((a) => [a.id, a])).values()];
  const matches = artifacts.filter((a) => (!kind || a.kind === kind) && a.title.toLocaleLowerCase().includes(search.toLocaleLowerCase()));
  const openChat = (sessionId: string, artifactId: string) => {
    setSession(sessionId);
    setArtifact(sessionId, artifactId);
    navigation.push(paths.assistant());
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader><Boxes className="mr-2 size-4 text-muted-foreground" /><span className="text-sm font-medium">{t(($) => $.nav.artifacts)}</span></PageHeader>
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <section className={cn("min-w-0 flex-1 overflow-y-auto p-4 md:p-6", selected && "hidden lg:block lg:max-w-[420px]")}>
          <div className="w-full">
            <div className="mb-7 flex flex-wrap items-start justify-between gap-4">
              <div><h1 className="text-2xl font-semibold tracking-tight">{t(($) => $.nav.artifacts)}</h1>
                <p className="mt-2 text-sm text-muted-foreground">{t(($) => $.artifacts_library.description)}</p></div>
              <Button variant="outline" disabled={createSession.isPending} onClick={() => createSession.mutate(undefined, { onSuccess: (session) => { setSession(session.id); navigation.push(paths.assistant()); } })}><Plus className="size-4" />{t(($) => $.artifacts_library.create)}</Button>
            </div>
            <div className="relative mb-4"><Search className="pointer-events-none absolute left-3 top-3 size-4 text-muted-foreground" /><Input className="pl-9" value={search} onChange={(e) => setSearch(e.target.value)} aria-label={t(($) => $.artifacts_library.search)} placeholder={t(($) => $.artifacts_library.search)} /></div>
            <div className="mb-6 flex flex-wrap gap-1" aria-label={t(($) => $.artifacts_library.filter)}>
              {["", "markdown", "table", "chart", "html"].map((value) => <Button key={value} size="sm" variant={kind === value ? "secondary" : "ghost"} aria-pressed={kind === value} onClick={() => setKind(value)}>{value ? kindLabel(value) : t(($) => $.artifacts_library.all)}</Button>)}
            </div>
            {query.isPending && <p role="status" className="py-10 text-sm text-muted-foreground">{t(($) => $.artifacts_library.loading)}</p>}
            {query.isError && <div role="alert" className="py-8"><p className="mb-3 text-sm">{t(($) => $.artifacts_library.error)}</p><Button variant="outline" onClick={() => void query.refetch()}>{t(($) => $.artifacts_library.retry)}</Button></div>}
            {!query.isPending && !query.isError && matches.length === 0 && <p className="py-12 text-sm text-muted-foreground">{artifacts.length ? t(($) => $.artifacts_library.no_results) : t(($) => $.artifacts_library.empty)}</p>}
            <div className={cn("grid gap-4", !selected && "sm:grid-cols-2 xl:grid-cols-3")}>
              {matches.map((artifact) => {
                const Icon = artifactKindIcon(artifact.kind);
                return <article key={artifact.id} className={cn("overflow-hidden rounded-xl border bg-card transition-colors hover:border-foreground/30", selected === artifact.id && "border-brand")}>
                  <button className="block w-full text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring" onClick={() => setSelected(artifact.id)}>
                    <div className="flex h-28 items-center justify-center border-b bg-muted/25"><Icon className="size-9 text-muted-foreground" strokeWidth={1.2} /></div>
                    <div className="p-4"><div className="mb-2 flex items-center justify-between text-xs text-muted-foreground"><span>{kindLabel(artifact.kind)}</span><span>{t(($) => $.artifacts_library.version, { version: artifact.version })}</span></div><h2 className="line-clamp-2 min-h-10 text-sm font-medium">{artifact.title || t(($) => $.artifacts_library.untitled)}</h2></div>
                  </button>
                  <div className="flex items-center justify-between border-t px-4 py-2"><time className="text-xs text-muted-foreground" dateTime={artifact.updated_at}>{Number.isFinite(Date.parse(artifact.updated_at)) ? new Date(artifact.updated_at).toLocaleDateString() : ""}</time><Button variant="ghost" size="sm" onClick={() => openChat(artifact.session_id, artifact.id)}><ArrowUpRight className="size-3.5" />{t(($) => $.artifacts_library.edit)}</Button></div>
                </article>;
              })}
            </div>
            {query.hasNextPage && <Button className="mt-5" variant="outline" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{t(($) => $.artifacts_library.more)}</Button>}
          </div>
        </section>
        {selected && <ArtifactPane key={selected} artifactId={selected} onClose={() => setSelected(null)} expanded onEdit={() => { const a = artifacts.find((item) => item.id === selected); if (a) openChat(a.session_id, a.id); }} />}
      </div>
    </div>
  );
}
