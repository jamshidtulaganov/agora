#!/usr/bin/env bash
# qa_switch.sh — switch (or restore) the per-dev sddev QA box to a branch via the
# qa_switch.php hook. Used by the agora-sddev-qa skill.
#
#   bash qa_switch.sh <branch> <remote>     remote: fork | origin
#   bash qa_switch.sh btx-53487 fork        # switch to the PR branch
#   bash qa_switch.sh billing origin        # restore the base branch
#
# Reads QA_SWITCH_URL + QA_SWITCH_TOKEN from the environment (the agent's
# custom_env). Prints the hook's JSON result; exits 0 only on {"ok":true}.
set -euo pipefail

branch="${1:?usage: qa_switch.sh <branch> <remote>}"
remote="${2:-fork}"

: "${QA_SWITCH_URL:?QA_SWITCH_URL not set}"
: "${QA_SWITCH_TOKEN:?QA_SWITCH_TOKEN not set}"

# Defend against injection: the hook also validates, but reject obviously bad
# input before it leaves the machine. Branch chars match the hook's regex.
case "$branch" in
  *[!A-Za-z0-9._/-]*) echo "qa_switch: invalid branch '$branch'" >&2; exit 2 ;;
esac
case "$remote" in
  fork|origin) ;;
  *) echo "qa_switch: remote must be fork|origin, got '$remote'" >&2; exit 2 ;;
esac

resp="$(curl -fsS --max-time 240 -X POST \
  -H "X-QA-Token: ${QA_SWITCH_TOKEN}" \
  "${QA_SWITCH_URL}?branch=${branch}&remote=${remote}")" || {
    echo "qa_switch: request to ${QA_SWITCH_URL} failed" >&2; exit 1; }

echo "$resp"
# Surface a hook-level failure ({"ok":false,...}) as a non-zero exit.
case "$resp" in
  *'"ok":true'*) exit 0 ;;
  *) echo "qa_switch: hook reported failure" >&2; exit 1 ;;
esac
