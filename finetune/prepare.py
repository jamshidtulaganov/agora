#!/usr/bin/env python3
"""Prepare an SFT dataset from Agora run traces.

Pulls GET /api/datasets/runs (the Slice-3 export), keeps the runs a human
accepted, and writes one chat record per run to JSONL — the shape Unsloth /
axolotl / TRL all read directly.

Usage:
  AGORA_URL=https://your-app.fly.dev \\
  AGORA_TOKEN=<personal access token> \\
  AGORA_WORKSPACE_ID=356e2301-... \\
  python finetune/prepare.py --outcome accepted --out train.jsonl

Stdlib only (urllib + json) — no pip install needed.
"""
import argparse
import json
import os
import sys
import urllib.parse
import urllib.request


def fetch_page(base, token, ws, outcome, limit, offset):
    q = {"limit": limit, "offset": offset}
    if outcome:
        q["outcome"] = outcome
    url = f"{base}/api/datasets/runs?" + urllib.parse.urlencode(q)
    req = urllib.request.Request(
        url,
        headers={"Authorization": f"Bearer {token}", "X-Workspace-ID": ws},
    )
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


def to_chat(ex):
    """Map one export Example -> a chat record.

    The assistant target is the agent's substantive text output; tool-call turns
    are summarized (truncated), not dumped raw. This shaping is the single
    biggest lever on fine-tune quality — tune it to what you want the model to
    learn to produce (e.g. only the final comment, or the full reasoning trace).
    """
    inp = ex.get("input", {})
    system = (inp.get("agent_instructions") or "").strip()
    user = "\n\n".join(
        p for p in (inp.get("issue_title", ""), inp.get("issue_description", "")) if p
    ).strip()

    parts = []
    for t in ex.get("output", []) or []:
        content = (t.get("content") or "").strip()
        if content:
            parts.append(content)
        elif t.get("tool"):
            out = (t.get("output") or "").strip()
            parts.append(f"[tool:{t['tool']}] {out[:500]}".strip())
    assistant = "\n".join(parts).strip()

    messages = []
    if system:
        messages.append({"role": "system", "content": system})
    messages.append({"role": "user", "content": user})
    messages.append({"role": "assistant", "content": assistant})
    return {"messages": messages, "outcome": ex.get("outcome"), "run_id": ex.get("run_id")}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--outcome", default="accepted",
                    help="filter: accepted|corrected|rejected, or '' for all")
    ap.add_argument("--out", default="train.jsonl")
    ap.add_argument("--page", type=int, default=200)
    args = ap.parse_args()

    base = os.environ.get("AGORA_URL", "").rstrip("/")
    token = os.environ.get("AGORA_TOKEN", "")
    ws = os.environ.get("AGORA_WORKSPACE_ID", "")
    if not (base and token and ws):
        sys.exit("set AGORA_URL, AGORA_TOKEN, AGORA_WORKSPACE_ID")

    offset = kept = seen = 0
    with open(args.out, "w", encoding="utf-8") as f:
        while True:
            data = fetch_page(base, token, ws, args.outcome, args.page, offset)
            examples = data.get("examples", [])
            if not examples:
                break
            for ex in examples:
                seen += 1
                rec = to_chat(ex)
                user = rec["messages"][-2]["content"]
                assistant = rec["messages"][-1]["content"]
                # Skip empty pairs — they teach the model nothing.
                if not user or not assistant:
                    continue
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
                kept += 1
            offset += len(examples)
            if len(examples) < args.page:
                break

    print(f"seen {seen} runs, wrote {kept} examples -> {args.out}", file=sys.stderr)


if __name__ == "__main__":
    main()
