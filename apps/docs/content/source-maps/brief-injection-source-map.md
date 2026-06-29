# brief-injection-source-map.md

Source-of-truth trace for `/developers/agentic/brief-injection`. Every claim on
the page maps to an exact symbol below. If any of these move, edit the page and
this sidecar in the same PR.

## server/internal/daemon/execenv/runtime_config.go

| Page claim | Symbol / lines | Verbatim anchor |
| --- | --- | --- |
| Marker constants; changing them is a breaking change | `runtimeMarkerBegin`, `runtimeMarkerEnd` (const block ~L29-31) | `"<!-- BEGIN MULTICA-RUNTIME (auto-managed; do not edit) -->"`, `"<!-- END MULTICA-RUNTIME -->"` |
| Managed separator is `"\n\n"`, part of managed region, distinguishes created vs pre-existing file | `runtimeManagedSeparator` (~L49) | `runtimeManagedSeparator = "\n\n"` |
| Inject builds content, resolves path, returns brief even when path `""` (prompt-only) | `InjectRuntimeConfig` (~L163-171) | `if path == "" { ... return content, nil }` |
| Provider→filename table; `""` for unknown | `runtimeConfigPath` (~L178-189) | `case "claude", "codebuddy": CLAUDE.md`; `case "codex", "copilot", "opencode", "openclaw", "hermes", "pi", "cursor", "kimi", "kiro", "antigravity": AGENTS.md`; `case "gemini": GEMINI.md`; `default: return ""` |
| Idempotent replace not overwrite; MUL-2753; three file states; block format; unconditional separator | `writeRuntimeConfigFile` (~L213-241) | `block := runtimeMarkerBegin + "\n" + strings.TrimRight(brief, "\n") + "\n" + runtimeMarkerEnd + "\n"`; `errors.Is(err, fs.ErrNotExist)` → write block only; replace branch `existingStr[:start] + block + existingStr[end:]`; append branch `existingStr+runtimeManagedSeparator+block` |
| END strictly after BEGIN; stray-END and crash-half-block cases; BEGIN-no-END = block-to-EOF; end consumes trailing newline | `locateMarkerBlock` (~L263-281) | `endRel := strings.Index(content[afterBegin:], runtimeMarkerEnd)`; `if endRel < 0 { return start, len(content), true }`; `if end < len(content) && content[end] == '\n' { end++ }` |
| Byte-exact rollback; three states; created-file via missing separator → os.Remove; pre-existing → write remainder verbatim; no normalisation | `CleanupRuntimeConfig` (~L315-360) | `hadManagedSeparator := strings.HasSuffix(pre, runtimeManagedSeparator)`; `if !hadManagedSeparator && remainder == "" { os.Remove(path) }`; else `os.WriteFile(path, []byte(remainder), 0o644)` |
| Brief content built by buildMetaSkillContent (referenced; detailed in brief-anatomy) | `buildMetaSkillContent` (~L364-789) | `b.WriteString("# Agora Agent Runtime\n\n")` |

## server/internal/daemon/daemon.go

| Page claim | Symbol / lines | Verbatim anchor |
| --- | --- | --- |
| Inject called in runTask after StartTask; error non-fatal | `runTask` (~L2835-2838) | `runtimeBrief, err := execenv.InjectRuntimeConfig(env.WorkDir, provider, taskCtx)` then `d.logger.Warn("execenv: inject runtime config failed (non-fatal)", ...)` |
| Cleanup only for local_directory; cloud workdirs reused/GC'd wholesale; rationale (stale issue/comment/reply context) | `runTask` defer (~L2851-2868) | `if env.LocalDirectory { defer func() { execenv.CleanupRuntimeConfig(...); execenv.CleanupSidecars(...) }() }` |
| Sidecar cleanup is a sibling pass (MUL-2784) | `execenv.CleanupSidecars(env.RootDir)` (~L2864) | comment `see MUL-2784` |
| Inline gate providers openclaw/kiro/kimi | `providerNeedsInlineSystemPrompt` (~L2630-2637) | `case "openclaw", "kiro", "kimi": return true` |
| Inline delivery sets execOpts.SystemPrompt = runtimeBrief; same payload as file | `runTask` (~L3092-3094) | `if providerNeedsInlineSystemPrompt(provider) { execOpts.SystemPrompt = runtimeBrief }` |
| Hermes intentionally excluded (ACP loads its own context; duplication bloats/triggers filters) | comment block (~L3088-3091) + `server/pkg/agent/hermes.go` L73-74, L326 | `b.cfg.Logger.Debug("hermes ignoring ExecOptions.SystemPrompt; using cwd-scoped context files", ...)` |
| Backend-specific consumption of SystemPrompt (prepend vs --append-system-prompt) | `server/pkg/agent/openclaw.go`, `kimi.go` L300-301 (prepend); `pi.go` L516-517, `codebuddy.go` L59-60 (`--append-system-prompt`) | `args = append(args, "--append-system-prompt", opts.SystemPrompt)` (pi/codebuddy); `userText = opts.SystemPrompt + "\n\n---\n\n" + prompt` (kimi) |

## Notes / caveats verified against code

- The three `providerNeedsInlineSystemPrompt` providers all map to `AGENTS.md` in `runtimeConfigPath`, so they get BOTH file and inline delivery (page states this explicitly). Verified by reading both functions.
- The page deliberately does NOT claim `--append-system-prompt` is universal: `providerNeedsInlineSystemPrompt` only flips `execOpts.SystemPrompt`; the flag mapping is per-backend (openclaw/kimi prepend; pi/codebuddy use the flag). kiro's backend consumption was not opened in detail — page scopes the flag claim to pi/codebuddy and "prepend" to openclaw/kimi, avoiding an unverified kiro claim.
- MUL-2753 (truncation), PR #3438 (separator/normalisation review), MUL-2784 (sidecar accumulation) are sourced from in-file comments, not invented.
