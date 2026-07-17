"use client";

import { useState } from "react";
import { Check, Copy, Terminal } from "lucide-react";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { CODE_LIGATURE_CLASS } from "@agora/ui/lib/code-style";
import { cn } from "@agora/ui/lib/utils";
import { copyText } from "@agora/ui/lib/clipboard";
import { useT } from "../../i18n";

const INSTALL_CMD =
  "curl -fsSL https://raw.githubusercontent.com/jamshidtulaganov/agora-cli/main/install.sh | bash";
const INSTALL_CMD_WINDOWS =
  "irm https://raw.githubusercontent.com/jamshidtulaganov/agora-cli/main/install.ps1 | iex";
// Remote deployment: pin the CLI to this server (otherwise `agora setup
// self-host` defaults to localhost:8080/:3000 and the daemon never connects).
const SETUP_CMD =
  "agora setup self-host --server-url https://sd-agora-web.fly.dev --app-url https://sd-agora-web.fly.dev";

function CopyButton({ text }: { text: string }) {
  const { t } = useT("onboarding");
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    void copyText(text).then((ok) => {
      if (!ok) return;
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      aria-label={t(($) => $.cli_install.copy_aria)}
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-success" />
      ) : (
        <Copy className="h-3.5 w-3.5" />
      )}
    </button>
  );
}

function CommandRow({ cmd }: { cmd: string }) {
  return (
    <div className="flex items-start gap-2 rounded-lg bg-muted px-3 py-2.5 font-mono text-sm">
      <Terminal className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <code
        className={cn(
          "min-w-0 flex-1 whitespace-pre-wrap break-all",
          CODE_LIGATURE_CLASS,
        )}
      >
        {cmd}
      </code>
      <CopyButton text={cmd} />
    </div>
  );
}

function Step({ n, label, cmd }: { n: number; label: string; cmd: string }) {
  return (
    <div>
      <p className="mb-1.5 text-xs font-medium text-foreground">
        {n}. {label}
      </p>
      <CommandRow cmd={cmd} />
    </div>
  );
}

/* Children are platform names — not translatable content. */
function OsCaption({ children }: { children: string }) {
  return (
    <p className="mb-1 text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
      {children}
    </p>
  );
}

/**
 * CLI install instructions — two copy-and-run commands. Hardcoded because
 * step 1 is the public install script; step 2 is `agora setup self-host`
 * — this is a self-hosted SD instance, so the CLI connects to the local
 * server (defaults to localhost:8080 / :3000). For a remote deployment,
 * append `--server-url <api> --app-url <app>` (and `--callback-host` when
 * setting up from a different machine than the server).
 */
export function CliInstallInstructions() {
  const { t } = useT("onboarding");
  return (
    <Card className="w-full">
      <CardContent className="space-y-4 pt-4">
        <p className="text-xs leading-[1.55] text-muted-foreground">
          {t(($) => $.cli_install.intro)}
        </p>
        <div>
          <p className="mb-1.5 text-xs font-medium text-foreground">
            1. {t(($) => $.cli_install.step1_label)}
          </p>
          <div className="space-y-2">
            <div>
              <OsCaption>{"macOS / Linux"}</OsCaption>
              <CommandRow cmd={INSTALL_CMD} />
            </div>
            <div>
              <OsCaption>{"Windows (PowerShell)"}</OsCaption>
              <CommandRow cmd={INSTALL_CMD_WINDOWS} />
            </div>
          </div>
        </div>
        <Step n={2} label={t(($) => $.cli_install.step2_label)} cmd={SETUP_CMD} />
      </CardContent>
    </Card>
  );
}
