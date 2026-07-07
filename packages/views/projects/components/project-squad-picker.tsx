"use client";

import { useState } from "react";
import { Users, Ban } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { squadListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { Popover, PopoverContent, PopoverTrigger } from "@agora/ui/components/ui/popover";
import type { Project, UpdateProjectRequest } from "@agora/core/types";
import { useT } from "../../i18n";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";

// Bind a project to a single squad (project.squad_id). When bound, only that
// squad's agents are auto-orchestrated on the project's issues; null = unbound
// (any agent/squad may work it). The backend UpdateProject seeds squad_id from
// the existing row, so setting it here never disturbs the lead and vice versa.
export function ProjectSquadPicker({ project, handleUpdate, renderTrigger, align = "start" }: {
  project: Project;
  handleUpdate: (data: UpdateProjectRequest) => void;
  renderTrigger: (squadName: string | null) => React.ReactElement;
  align?: "start" | "end" | "center";
}) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const { data: squads = [] } = useQuery(squadListOptions(wsId));

  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const q = filter.toLowerCase();

  const active = squads.filter((s) => !s.archived_at);
  const filtered = active.filter((s) => s.name.toLowerCase().includes(q) || matchesPinyin(s.name, q));
  const currentName = project.squad_id ? (squads.find((s) => s.id === project.squad_id)?.name ?? null) : null;

  return (
    <Popover open={open} onOpenChange={(v) => { setOpen(v); if (!v) setFilter(""); }}>
      <PopoverTrigger render={renderTrigger(currentName)} />
      <PopoverContent align={align} className="w-52 p-0">
        <div className="px-2 py-1.5 border-b">
          <input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t(($) => $.squad.assign_placeholder)}
            className="w-full bg-transparent text-sm placeholder:text-muted-foreground outline-none"
          />
        </div>
        <div className="p-1 max-h-60 overflow-y-auto">
          <button
            type="button"
            onClick={() => { handleUpdate({ squad_id: null }); setOpen(false); }}
            className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent transition-colors"
          >
            <Ban className="h-3.5 w-3.5 text-muted-foreground" />
            <span className="text-muted-foreground">{t(($) => $.squad.no_squad)}</span>
          </button>
          {filtered.map((s) => (
            <button
              type="button"
              key={s.id}
              onClick={() => { handleUpdate({ squad_id: s.id }); setOpen(false); }}
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent transition-colors"
            >
              <Users className="h-3.5 w-3.5 text-muted-foreground" />
              <span>{s.name}</span>
            </button>
          ))}
          {filtered.length === 0 && filter && (
            <div className="px-2 py-3 text-center text-sm text-muted-foreground">{t(($) => $.squad.no_results)}</div>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
