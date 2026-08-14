"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, MonitorSmartphone, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  projectDevServersOptions,
  useDeleteMyProjectDevServer,
  useSetMyProjectDevServer,
} from "@agora/core/projects";
import { memberListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { useAuthStore } from "@agora/core/auth";
import { useT } from "../../i18n";

// Per-developer standing dev servers ("preview per project → per user").
// Each member declares THEIR OWN deployed box for this project (e.g.
// https://you.sdteam.uz) — issues assigned to them (or their agents) resolve
// QA preview + smoke to that box. Teammates' rows are read-only by design;
// the server enforces self-only writes.
export function ProjectDevServersSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const [open, setOpen] = useState(false);

  const { data: servers = [] } = useQuery({
    ...projectDevServersOptions(wsId, projectId),
    enabled: open,
  });
  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: open,
  });
  const setMine = useSetMyProjectDevServer(wsId, projectId);
  const deleteMine = useDeleteMyProjectDevServer(wsId, projectId);

  const nameByUserId = new Map<string, string>();
  for (const m of members) {
    nameByUserId.set(m.user_id, m.name || m.email);
  }

  const mine = servers.find((s) => s.user_id === user?.id);
  const others = servers.filter((s) => s.user_id !== user?.id);
  const savedURL = mine?.base_url ?? "";
  const [draft, setDraft] = useState(savedURL);
  useEffect(() => {
    setDraft(savedURL);
  }, [savedURL]);

  const saveMine = async () => {
    const next = draft.trim();
    if (next === savedURL) return;
    try {
      if (next === "") {
        if (savedURL !== "") {
          await deleteMine.mutateAsync();
          toast.success(t(($) => $.dev_servers.toast_removed));
        }
        return;
      }
      await setMine.mutateAsync(next);
      toast.success(t(($) => $.dev_servers.toast_saved));
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.dev_servers.toast_failed),
      );
      setDraft(savedURL);
    }
  };

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        <MonitorSmartphone className="!size-3 shrink-0 text-muted-foreground" />
        {t(($) => $.dev_servers.section_header)}
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
        />
      </button>
      {open && (
        <div className="space-y-2 pl-2">
          <p className="text-[10px] text-muted-foreground">
            {t(($) => $.dev_servers.description)}
          </p>
          <label className="block space-y-1">
            <span className="text-[10px] font-medium text-muted-foreground">
              {t(($) => $.dev_servers.mine_label)}
            </span>
            <div className="flex items-center gap-1.5">
              <input
                type="text"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onBlur={() => void saveMine()}
                onKeyDown={(e) => {
                  if (e.key === "Enter") (e.target as HTMLInputElement).blur();
                }}
                placeholder={t(($) => $.dev_servers.placeholder)}
                className="h-7 min-w-0 flex-1 rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
              />
              {savedURL !== "" && (
                <button
                  type="button"
                  aria-label={t(($) => $.dev_servers.remove_tooltip)}
                  className="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                  onClick={() => {
                    setDraft("");
                    void deleteMine
                      .mutateAsync()
                      .then(() => toast.success(t(($) => $.dev_servers.toast_removed)))
                      .catch(() => toast.error(t(($) => $.dev_servers.toast_failed)));
                  }}
                >
                  <Trash2 className="size-3.5" />
                </button>
              )}
            </div>
          </label>
          {others.length > 0 && (
            <ul className="space-y-1 border-t pt-2">
              {others.map((s) => (
                <li
                  key={s.user_id}
                  className="flex items-center justify-between gap-2 text-[11px]"
                >
                  <span className="shrink-0 text-muted-foreground">
                    {nameByUserId.get(s.user_id) ?? s.user_id.slice(0, 8)}
                  </span>
                  <a
                    href={s.base_url}
                    target="_blank"
                    rel="noreferrer"
                    className="truncate text-foreground/80 underline-offset-2 hover:underline"
                  >
                    {s.base_url}
                  </a>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
