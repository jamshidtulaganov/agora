"use client";

import { useEffect, useState } from "react";
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
import { Button } from "@agora/ui/components/ui/button";
import { isImeComposing } from "@agora/core/utils";
import { useT } from "../../i18n";

export function DeleteAccountDialog({
  email,
  loading = false,
  open,
  onOpenChange,
  onConfirm,
}: {
  email: string;
  loading?: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  const { t } = useT("settings");
  const [typed, setTyped] = useState("");
  const matched = typed === email;

  useEffect(() => {
    setTyped("");
  }, [email, open]);

  const submit = () => {
    if (!matched || loading) return;
    onConfirm();
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!loading) onOpenChange(nextOpen);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.delete_account_dialog.title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.delete_account_dialog.description)}
          </DialogDescription>
        </DialogHeader>

        <p className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-muted-foreground">
          {t(($) => $.delete_account_dialog.ownership_notice)}
        </p>

        <div className="space-y-2">
          <Label htmlFor="delete-account-confirm" className="text-xs">
            {t(($) => $.delete_account_dialog.type_to_confirm_prefix)}{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
              {email}
            </code>{" "}
            {t(($) => $.delete_account_dialog.type_to_confirm_suffix)}
          </Label>
          <Input
            id="delete-account-confirm"
            type="email"
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
            onKeyDown={(event) => {
              if (isImeComposing(event)) return;
              if (event.key === "Enter") {
                event.preventDefault();
                submit();
              }
            }}
            placeholder={email}
            autoFocus
            disabled={loading}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            spellCheck={false}
          />
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={loading}
          >
            {t(($) => $.delete_account_dialog.cancel)}
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={submit}
            disabled={!matched || loading}
          >
            {loading
              ? t(($) => $.delete_account_dialog.deleting)
              : t(($) => $.delete_account_dialog.confirm)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
