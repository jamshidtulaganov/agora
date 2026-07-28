"use client";

import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, Plus, Trash2 } from "lucide-react";
// Named import, NOT default: react-qr-code is CJS, and electron-vite's
// dep-optimizer default-import interop hands back the module namespace object
// instead of the component, throwing "Element type is invalid" the moment
// <QRCode> mounts. See the same note in lark-tab.tsx.
import { QRCode } from "react-qr-code";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Input } from "@agora/ui/components/ui/input";
import { Label } from "@agora/ui/components/ui/label";
import { Textarea } from "@agora/ui/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agora/ui/components/ui/select";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { memberListOptions } from "@agora/core/workspace/queries";
import { useActorName } from "@agora/core/workspace/hooks";
import { telegramInstallationsOptions, telegramKeys } from "@agora/core/telegram";
import { api } from "@agora/core/api";
import type { TelegramInstallation } from "@agora/core/types";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";

// The workspace settings panel for per-agent Telegram bots.
//
// Listing is member-visible; every write is admin-only. The backend enforces
// that independently — the UI hides the controls so a member is not shown a
// button that will refuse them.
//
// Unlike Lark, installing is NOT a device flow: Telegram has no such handshake,
// so an operator creates the bot in BotFather and pastes its token. The token
// is sealed server-side and never returned, so this panel can show which bot an
// agent owns but never the credential itself.
export function TelegramTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);

  const { data: members } = useQuery(memberListOptions(wsId));
  const canManage = useMemo(() => {
    const me = members?.find((m) => m.user_id === currentUser?.id);
    return me?.role === "owner" || me?.role === "admin";
  }, [members, currentUser?.id]);

  const { data, isLoading } = useQuery({
    ...telegramInstallationsOptions(wsId),
    enabled: !!wsId,
  });
  const installations = data?.installations ?? [];
  // Explicit === true: a missing field must read as "not configured", never as
  // an install form that cannot succeed.
  const configured = data?.configured === true;

  const [installOpen, setInstallOpen] = useState(false);
  const [disconnectTarget, setDisconnectTarget] = useState<TelegramInstallation | null>(null);
  const [disconnecting, setDisconnecting] = useState(false);

  const refresh = () => qc.invalidateQueries({ queryKey: telegramKeys.installations(wsId) });

  const handleDisconnect = async () => {
    if (!disconnectTarget) return;
    setDisconnecting(true);
    try {
      await api.deleteAgentTelegramBot(wsId, disconnectTarget.agent_id);
      await refresh();
      toast.success(t(($) => $.telegram.toast_disconnected));
      setDisconnectTarget(null);
    } catch {
      toast.error(t(($) => $.telegram.toast_disconnect_failed));
    } finally {
      setDisconnecting(false);
    }
  };

  if (!configured && installations.length === 0) {
    return (
      <Card>
        <CardContent className="py-6">
          <p className="text-sm font-medium">{t(($) => $.telegram.not_configured_title)}</p>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(($) => $.telegram.not_configured_description)}
          </p>
        </CardContent>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm text-muted-foreground">{t(($) => $.telegram.page_description)}</p>
        {canManage && configured ? (
          <Button size="sm" onClick={() => setInstallOpen(true)}>
            <Plus className="mr-1.5 size-4" />
            {t(($) => $.telegram.connect_button)}
          </Button>
        ) : null}
      </div>

      {isLoading ? (
        <Card>
          <CardContent className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            {t(($) => $.telegram.loading)}
          </CardContent>
        </Card>
      ) : installations.length === 0 ? (
        <Card>
          <CardContent className="py-6">
            <p className="text-sm font-medium">{t(($) => $.telegram.empty_title)}</p>
            <p className="mt-1 text-sm text-muted-foreground">
              {t(($) => $.telegram.empty_description)}
            </p>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="divide-y p-0">
            {installations.map((installation) => (
              <InstallationRow
                key={installation.agent_id}
                installation={installation}
                canManage={canManage}
                onDisconnect={() => setDisconnectTarget(installation)}
                onChanged={refresh}
              />
            ))}
          </CardContent>
        </Card>
      )}

      {installOpen ? (
        <InstallDialog
          onClose={() => setInstallOpen(false)}
          onInstalled={async () => {
            await refresh();
            setInstallOpen(false);
          }}
        />
      ) : null}

      <AlertDialog
        open={!!disconnectTarget}
        onOpenChange={(open) => !open && setDisconnectTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.telegram.disconnect_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.telegram.disconnect_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={disconnecting}>
              {t(($) => $.telegram.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleDisconnect} disabled={disconnecting}>
              {disconnecting ? (
                <Loader2 className="mr-1.5 size-4 animate-spin" />
              ) : (
                <Trash2 className="mr-1.5 size-4" />
              )}
              {t(($) => $.telegram.disconnect_confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// One connected bot: which agent owns it, which groups may reach it, and the
// two controls that change that — add a group by QR, and edit who may talk.
function InstallationRow({
  installation,
  canManage,
  onDisconnect,
  onChanged,
}: {
  installation: TelegramInstallation;
  canManage: boolean;
  onDisconnect: () => void;
  onChanged: () => void;
}) {
  const { t } = useT("settings");
  const { getAgentName } = useActorName();
  const [bindOpen, setBindOpen] = useState(false);
  const [accessOpen, setAccessOpen] = useState(false);

  const isActive = installation.status === "active";
  const chatCount = installation.allowed_chat_ids?.length ?? 0;

  return (
    <div className="flex flex-wrap items-center gap-3 px-4 py-3">
      <ActorAvatar actorType="agent" actorId={installation.agent_id} size={24} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">
            {getAgentName(installation.agent_id)}
          </span>
          {isActive ? (
            <span className="size-1.5 shrink-0 rounded-full bg-emerald-500" />
          ) : (
            <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
              {t(($) => $.telegram.status_revoked)}
            </span>
          )}
        </div>
        <p className="truncate text-xs text-muted-foreground">
          @{installation.bot_username} · {policyLabel(t, installation.access_policy)} ·{" "}
          {t(($) => $.telegram.groups_count, { count: chatCount })}
        </p>
      </div>
      {canManage && isActive ? (
        <div className="flex items-center gap-1.5">
          <Button size="sm" variant="outline" onClick={() => setBindOpen(true)}>
            {t(($) => $.telegram.add_group)}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setAccessOpen(true)}>
            {t(($) => $.telegram.manage_access)}
          </Button>
          <Button size="sm" variant="ghost" onClick={onDisconnect}>
            <Trash2 className="size-4" />
          </Button>
        </div>
      ) : null}

      {bindOpen ? (
        <BindGroupDialog
          agentId={installation.agent_id}
          onClose={() => setBindOpen(false)}
          onBound={onChanged}
        />
      ) : null}
      {accessOpen ? (
        <AccessDialog
          installation={installation}
          onClose={() => setAccessOpen(false)}
          onSaved={() => {
            onChanged();
            setAccessOpen(false);
          }}
        />
      ) : null}
    </div>
  );
}

// Install: pick an agent, paste the BotFather token.
//
// The agent list is filtered to agents that do NOT already have a bot — the
// backend keys the installation on agent_id, so choosing an occupied agent
// would silently replace its bot rather than add one.
function InstallDialog({
  onClose,
  onInstalled,
}: {
  onClose: () => void;
  onInstalled: () => void | Promise<void>;
}) {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  // Queried directly rather than through a shared factory: this is the only
  // place that needs the full agent list, and the dialog is short-lived.
  const { data: agents } = useQuery({
    queryKey: ["agents", wsId, "telegram-install-picker"],
    queryFn: () => api.listAgents({ workspace_id: wsId }),
    enabled: !!wsId,
  });
  const { data } = useQuery(telegramInstallationsOptions(wsId));
  const taken = new Set((data?.installations ?? []).map((i) => i.agent_id));

  const available = (agents ?? []).filter((a) => !taken.has(a.id));
  const [agentId, setAgentId] = useState("");
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    if (!agentId || !token.trim()) return;
    setSaving(true);
    try {
      await api.installAgentTelegramBot(wsId, agentId, token.trim());
      // Clear the token from component state immediately: it grants full
      // control of the bot, and there is no reason for it to outlive the
      // request that consumed it.
      setToken("");
      toast.success(t(($) => $.telegram.toast_connected));
      await onInstalled();
    } catch {
      toast.error(t(($) => $.telegram.toast_connect_failed));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.telegram.install_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.telegram.install_description)}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="telegram-install-agent">{t(($) => $.telegram.install_agent_label)}</Label>
            <Select value={agentId} onValueChange={(v) => setAgentId(v ?? "")}>
              <SelectTrigger id="telegram-install-agent">
                <SelectValue placeholder={t(($) => $.telegram.install_agent_placeholder)} />
              </SelectTrigger>
              <SelectContent>
                {available.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {available.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.telegram.install_no_agents)}
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="telegram-install-token">{t(($) => $.telegram.install_token_label)}</Label>
            <Input
              id="telegram-install-token"
              type="password"
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="123456789:AA..."
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.telegram.install_token_hint)}
            </p>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            {t(($) => $.telegram.cancel)}
          </Button>
          <Button onClick={submit} disabled={saving || !agentId || !token.trim()}>
            {saving ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
            {t(($) => $.telegram.install_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// Bind a group by QR.
//
// Adding a bot to a group cannot authorize that group by itself — anyone can
// invite a bot anywhere — so the link carries a single-use token minted here,
// server-side, by an owner/admin. Scanning it opens Telegram's group picker;
// the bot is bound when it arrives carrying the token.
function BindGroupDialog({
  agentId,
  onClose,
  onBound,
}: {
  agentId: string;
  onClose: () => void;
  onBound: () => void;
}) {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const [link, setLink] = useState<{ url: string; bot: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  const mint = async () => {
    setLoading(true);
    setFailed(false);
    try {
      const res = await api.createAgentTelegramBindLink(wsId, agentId);
      setLink({ url: res.group_url, bot: res.bot_username });
    } catch {
      setFailed(true);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.telegram.bind_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.telegram.bind_description)}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col items-center gap-3 py-2">
          {link ? (
            <>
              <div className="rounded-md border bg-white p-3">
                <QRCode value={link.url} size={180} />
              </div>
              {/* A link as well as the code: the operator is often already on
                  the machine showing the QR, and photographing your own screen
                  is a needless step. */}
              <a
                href={link.url}
                target="_blank"
                rel="noreferrer"
                className="text-xs text-primary underline"
              >
                {t(($) => $.telegram.bind_open_link)}
              </a>
              <p className="text-center text-xs text-muted-foreground">
                {t(($) => $.telegram.bind_expiry_hint)}
              </p>
            </>
          ) : failed ? (
            <p className="text-sm text-destructive">{t(($) => $.telegram.bind_failed)}</p>
          ) : (
            <Button onClick={mint} disabled={loading}>
              {loading ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
              {t(($) => $.telegram.bind_generate)}
            </Button>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t(($) => $.telegram.close)}
          </Button>
          {link ? (
            <Button
              onClick={() => {
                onBound();
                onClose();
              }}
            >
              {t(($) => $.telegram.bind_done)}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// Who may instruct the agent through this bot.
//
// Two lists, deliberately not one: allowed users may ASK the agent, while the
// commands that widen access (/allow, /deny in the group) are gated on the
// caller's Agora workspace role — being able to ask must not imply being able
// to hand that power to someone else.
function AccessDialog({
  installation,
  onClose,
  onSaved,
}: {
  installation: TelegramInstallation;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const [policy, setPolicy] = useState(normalizePolicy(installation.access_policy));
  const [users, setUsers] = useState((installation.allowed_user_ids ?? []).join("\n"));
  const [chats, setChats] = useState((installation.allowed_chat_ids ?? []).join("\n"));
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await api.setAgentTelegramAccess(wsId, installation.agent_id, {
        policy,
        allowed_user_ids: splitIds(users),
        allowed_chat_ids: splitIds(chats),
      });
      toast.success(t(($) => $.telegram.toast_access_saved));
      onSaved();
    } catch {
      toast.error(t(($) => $.telegram.toast_access_failed));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.telegram.access_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.telegram.access_description)}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="telegram-access-policy">{t(($) => $.telegram.access_policy_label)}</Label>
            <Select value={policy} onValueChange={(v) => setPolicy(normalizePolicy(v ?? ""))}>
              <SelectTrigger id="telegram-access-policy">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="closed">{t(($) => $.telegram.policy_closed)}</SelectItem>
                <SelectItem value="allowlist">{t(($) => $.telegram.policy_allowlist)}</SelectItem>
                <SelectItem value="open">{t(($) => $.telegram.policy_open)}</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.telegram.policy_hint)}
            </p>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="telegram-access-chats">{t(($) => $.telegram.access_chats_label)}</Label>
            <Textarea
              id="telegram-access-chats"
              rows={3}
              value={chats}
              onChange={(e) => setChats(e.target.value)}
              placeholder="-1001234567890"
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.telegram.access_chats_hint)}
            </p>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="telegram-access-users">{t(($) => $.telegram.access_users_label)}</Label>
            <Textarea
              id="telegram-access-users"
              rows={3}
              value={users}
              onChange={(e) => setUsers(e.target.value)}
              placeholder="905434593"
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.telegram.access_users_hint)}
            </p>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            {t(($) => $.telegram.cancel)}
          </Button>
          <Button onClick={save} disabled={saving}>
            {saving ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
            {t(($) => $.telegram.access_save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** splitIds turns a textarea into a clean id list. Accepts newlines, commas or
 * spaces because people paste from all three, and drops empties so a trailing
 * newline is not submitted as an id the backend then rejects. */
function splitIds(raw: string): string[] {
  return raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** normalizePolicy keeps an unrecognised server value from selecting nothing in
 * the dropdown. Falls back to the safe end: a policy we cannot read must not
 * present itself as `open`. */
function normalizePolicy(value: string): "closed" | "allowlist" | "open" {
  return value === "allowlist" || value === "open" ? value : "closed";
}

/** policyLabel renders the policy for the row summary, with a generic fallback
 * so a value from a newer server downgrades instead of showing a blank. */
function policyLabel(t: ReturnType<typeof useT<"settings">>["t"], policy: string): string {
  switch (policy) {
    case "open":
      return t(($) => $.telegram.policy_open);
    case "allowlist":
      return t(($) => $.telegram.policy_allowlist);
    case "closed":
      return t(($) => $.telegram.policy_closed);
    default:
      return t(($) => $.telegram.policy_unknown);
  }
}
