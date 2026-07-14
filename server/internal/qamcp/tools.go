package qamcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon"
)

// The four QA tools. Every result is a JSON object string; every verdict in it
// traces to a real process exit code.
//
//   - detect_tests:     which runner this repo uses + the command to run it
//   - run_tests:        run the suite (detected or given) → {exit_code, passed, output}
//   - run_case_script:  run ONE self-contained case script (playwright/node) with
//     optional TRACE_PATH capture → {exit_code, passed, output}
//   - write_test_file:  materialize a test file into the repo (guarded), so the
//     suite ACCRETES as committed, CI-runnable coverage instead of living only
//     in DB rows
const (
	defaultRunTimeout = 5 * time.Minute
	maxRunTimeout     = 15 * time.Minute
)

func toolDefinitions() []map[string]any {
	obj := func(props map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

	return []map[string]any{
		{
			"name":        "detect_tests",
			"description": "Detect the repo's test runner and the exact command that runs its suite (package.json test script / Makefile test / go test / composer test). Returns {command, framework}; empty command = no tests configured.",
			"inputSchema": obj(map[string]any{
				"dir": str("Repo directory. Defaults to the current working directory."),
			}),
		},
		{
			"name":        "run_tests",
			"description": "Run the repo's test suite as a plain deterministic command and report the REAL exit code. Uses the detected test command unless `command` overrides it. Pass `changed_files` (from your `git diff --name-only`) to run ONLY the tests related to the change — vitest related / jest --findRelatedTests / go test on the changed packages — instead of the whole suite; the result's `selection` field says whether related-selection applied or it fell back to the full suite. Prefer this over composing shell yourself: the verdict is {exit_code, passed, output} — passed is exit_code==0, never an opinion.",
			"inputSchema": obj(map[string]any{
				"dir":             str("Repo directory. Defaults to the current working directory."),
				"command":         str("Override test command (e.g. 'pnpm vitest run path/to/file'). Empty = auto-detect."),
				"changed_files":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Repo-relative changed files (git diff --name-only). When set, run only tests RELATED to these files (import-graph selection where the runner supports it)."},
				"timeout_seconds": num("Kill the run after this many seconds (default 300, max 900)."),
			}),
		},
		{
			"name":        "run_case_script",
			"description": "Run ONE self-contained test-case script (a Playwright/node ESM module that exits 0 on pass, 1 on fail) and report the REAL exit code. Pass the script source inline; it is written to a temp file, run with `node`, and cleaned up. Set trace_path to capture a Playwright trace (the script must honor process.env.TRACE_PATH).",
			"inputSchema": obj(map[string]any{
				"script":          str("The complete script source (ESM, plain node). REQUIRED."),
				"dir":             str("Working directory for the run. Defaults to the current working directory."),
				"trace_path":      str("Absolute path for the Playwright trace .zip; exported to the script as TRACE_PATH."),
				"timeout_seconds": num("Kill the run after this many seconds (default 300, max 900)."),
			}, "script"),
		},
		{
			"name":        "write_test_file",
			"description": "Write a test file into the repo so the suite accretes as committed, CI-runnable coverage. Path must be relative, inside the repo, and look like a test file (contain 'test' or 'spec', or live under a test directory). Refuses to overwrite an existing file unless overwrite=true.",
			"inputSchema": obj(map[string]any{
				"path":      str("Repo-relative path of the test file (e.g. 'tests/greet-button.spec.ts'). REQUIRED."),
				"content":   str("The full file content. REQUIRED."),
				"dir":       str("Repo directory. Defaults to the current working directory."),
				"overwrite": boolean("Allow replacing an existing file (default false)."),
			}, "path", "content"),
		},
	}
}

// dispatchTool routes one tools/call. The returned string is a JSON object.
func dispatchTool(name string, args json.RawMessage, logger *slog.Logger) (string, error) {
	switch name {
	case "detect_tests":
		return toolDetectTests(args)
	case "run_tests":
		return toolRunTests(args, logger)
	case "run_case_script":
		return toolRunCaseScript(args, logger)
	case "write_test_file":
		return toolWriteTestFile(args)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

// resolveDir defaults to cwd and requires the directory to exist.
func resolveDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return os.Getwd()
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("dir %q is not a directory", dir)
	}
	return dir, nil
}

func clampTimeout(seconds float64) time.Duration {
	if seconds <= 0 {
		return defaultRunTimeout
	}
	d := time.Duration(seconds * float64(time.Second))
	if d > maxRunTimeout {
		return maxRunTimeout
	}
	return d
}

// frameworkFromCommand labels the runner from its command string — a hint for
// the agent, not a contract.
func frameworkFromCommand(cmd string) string {
	c := strings.ToLower(cmd)
	switch {
	case strings.Contains(c, "vitest"):
		return "vitest"
	case strings.Contains(c, "jest"):
		return "jest"
	case strings.Contains(c, "playwright"):
		return "playwright"
	case strings.Contains(c, "go test"):
		return "go"
	case strings.Contains(c, "composer") || strings.Contains(c, "phpunit") || strings.Contains(c, "codecept"):
		return "php"
	case strings.Contains(c, "make test"):
		return "make"
	case cmd == "":
		return ""
	default:
		return "unknown"
	}
}

func toolDetectTests(args json.RawMessage) (string, error) {
	var in struct {
		Dir string `json:"dir"`
	}
	_ = json.Unmarshal(args, &in)
	dir, err := resolveDir(in.Dir)
	if err != nil {
		return "", err
	}
	cmd := daemon.DetectTestCommand(dir)
	// package.json "test" script resolution may hide the actual runner — peek
	// into the script body for a better framework label when possible.
	framework := frameworkFromCommand(cmd)
	if framework == "unknown" || framework == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(b, &pkg) == nil {
				if f := frameworkFromCommand(pkg.Scripts["test"]); f != "" && f != "unknown" {
					framework = f
				}
			}
		}
	}
	out, _ := json.Marshal(map[string]any{"command": cmd, "framework": framework, "dir": dir})
	return string(out), nil
}

// relatedTestCommand builds a runner-native "only tests related to these
// files" command (the A1 selection lever): vitest `related`, jest
// `--findRelatedTests` (both walk the import graph), or `go test` on the
// changed packages. Returns ok=false when the framework has no selection mode
// (caller falls back to the full suite) or no usable file survives filtering.
func relatedTestCommand(baseCmd, framework string, files []string) (string, bool) {
	quoted := make([]string, 0, len(files))
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		quoted = append(quoted, shellQuote(f))
	}
	if len(quoted) == 0 {
		return "", false
	}
	// Package-manager prefix from the detected command ("pnpm run test" → pnpm).
	pm := "npx"
	if fields := strings.Fields(baseCmd); len(fields) > 0 {
		switch fields[0] {
		case "pnpm", "yarn", "bun":
			pm = fields[0] + " exec"
		case "npm":
			pm = "npx"
		}
	}
	switch framework {
	case "vitest":
		return pm + " vitest related --run " + strings.Join(quoted, " "), true
	case "jest":
		return pm + " jest --findRelatedTests " + strings.Join(quoted, " ") + " --passWithNoTests", true
	case "go":
		// Changed .go files → their packages. Non-Go files don't select tests.
		dirs := map[string]bool{}
		for _, f := range files {
			f = strings.TrimSpace(f)
			if !strings.HasSuffix(f, ".go") {
				continue
			}
			d := filepath.Dir(filepath.ToSlash(f))
			if d == "." {
				dirs["./."] = true
			} else {
				dirs["./"+d] = true
			}
		}
		if len(dirs) == 0 {
			return "", false
		}
		pkgs := make([]string, 0, len(dirs))
		for d := range dirs {
			pkgs = append(pkgs, shellQuote(d))
		}
		sort.Strings(pkgs)
		return "go test " + strings.Join(pkgs, " "), true
	default:
		return "", false
	}
}

func toolRunTests(args json.RawMessage, logger *slog.Logger) (string, error) {
	var in struct {
		Dir            string   `json:"dir"`
		Command        string   `json:"command"`
		ChangedFiles   []string `json:"changed_files"`
		TimeoutSeconds float64  `json:"timeout_seconds"`
	}
	_ = json.Unmarshal(args, &in)
	dir, err := resolveDir(in.Dir)
	if err != nil {
		return "", err
	}
	command := strings.TrimSpace(in.Command)
	detected := daemon.DetectTestCommand(dir)
	if command == "" {
		command = detected
	}
	if command == "" {
		out, _ := json.Marshal(map[string]any{
			"exit_code": -1, "passed": false, "command": "",
			"output": "no test command configured for this repo (no package.json test script, Makefile test target, go.mod, or composer test script)",
		})
		return string(out), nil
	}
	// Related-test selection: only when the caller didn't hand-craft a command
	// (an explicit command wins verbatim) and the runner supports it.
	selection := "full"
	if len(in.ChangedFiles) > 0 && strings.TrimSpace(in.Command) == "" {
		framework := frameworkFromCommand(detected)
		if framework == "unknown" || framework == "" {
			if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
				var pkg struct {
					Scripts map[string]string `json:"scripts"`
				}
				if json.Unmarshal(b, &pkg) == nil {
					if f := frameworkFromCommand(pkg.Scripts["test"]); f != "" && f != "unknown" {
						framework = f
					}
				}
			}
		}
		if related, ok := relatedTestCommand(detected, framework, in.ChangedFiles); ok {
			command = related
			selection = "related"
		}
	}
	logger.Info("qamcp: run_tests", "dir", dir, "command", command, "selection", selection)
	output, code := daemon.RunProjectTests(dir, command, clampTimeout(in.TimeoutSeconds))
	out, _ := json.Marshal(map[string]any{
		"command": command, "exit_code": code, "passed": code == 0, "output": output,
		"selection": selection,
	})
	return string(out), nil
}

func toolRunCaseScript(args json.RawMessage, logger *slog.Logger) (string, error) {
	var in struct {
		Script         string  `json:"script"`
		Dir            string  `json:"dir"`
		TracePath      string  `json:"trace_path"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(in.Script) == "" {
		return "", fmt.Errorf("script is required")
	}
	dir, err := resolveDir(in.Dir)
	if err != nil {
		return "", err
	}
	// The temp module lives INSIDE the workdir so relative imports/node_modules
	// resolution behave exactly as a committed test would.
	tmp, err := os.CreateTemp(dir, ".agora-qa-case-*.mjs")
	if err != nil {
		return "", fmt.Errorf("write case script: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(in.Script); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write case script: %w", err)
	}
	tmp.Close()

	command := "node " + shellQuote(tmpPath)
	if strings.TrimSpace(in.TracePath) != "" {
		command = "TRACE_PATH=" + shellQuote(strings.TrimSpace(in.TracePath)) + " " + command
	}
	logger.Info("qamcp: run_case_script", "dir", dir, "trace", in.TracePath != "")
	output, code := daemon.RunProjectTests(dir, command, clampTimeout(in.TimeoutSeconds))
	result := map[string]any{"exit_code": code, "passed": code == 0, "output": output}
	if strings.TrimSpace(in.TracePath) != "" {
		if _, statErr := os.Stat(strings.TrimSpace(in.TracePath)); statErr == nil {
			result["trace_path"] = strings.TrimSpace(in.TracePath)
		}
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// looksLikeTestPath gates write_test_file to test-looking destinations so the
// tool can't be used as a generic code-write escape hatch.
func looksLikeTestPath(rel string) bool {
	lower := strings.ToLower(filepath.ToSlash(rel))
	base := filepath.Base(lower)
	if strings.Contains(base, "test") || strings.Contains(base, "spec") {
		return true
	}
	for _, seg := range strings.Split(filepath.Dir(lower), "/") {
		switch seg {
		case "test", "tests", "__tests__", "spec", "specs", "e2e", "qa":
			return true
		}
	}
	return false
}

func toolWriteTestFile(args json.RawMessage) (string, error) {
	var in struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Dir       string `json:"dir"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" || in.Content == "" {
		return "", fmt.Errorf("path and content are required")
	}
	dir, err := resolveDir(in.Dir)
	if err != nil {
		return "", err
	}
	rel := filepath.Clean(strings.TrimSpace(in.Path))
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path must be relative and inside the repo: %q", in.Path)
	}
	if !looksLikeTestPath(rel) {
		return "", fmt.Errorf("path %q does not look like a test file — name it *test*/*spec* or place it under a test directory", rel)
	}
	abs := filepath.Join(dir, rel)
	// Defense in depth: the joined path must still resolve inside dir.
	if resolved, err := filepath.Abs(abs); err != nil || !strings.HasPrefix(resolved+string(filepath.Separator), mustAbs(dir)+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the repo: %q", in.Path)
	}
	if _, statErr := os.Stat(abs); statErr == nil && !in.Overwrite {
		return "", fmt.Errorf("file %q already exists — pass overwrite=true to replace it", rel)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("create test dir: %w", err)
	}
	if err := os.WriteFile(abs, []byte(in.Content), 0o644); err != nil {
		return "", fmt.Errorf("write test file: %w", err)
	}
	out, _ := json.Marshal(map[string]any{"written": rel, "bytes": len(in.Content)})
	return string(out), nil
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// shellQuote single-quotes s for POSIX shells (the run goes through `$SHELL -lc`).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
