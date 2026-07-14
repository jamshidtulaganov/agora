package qamcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// drive runs the server over an in-memory stdio pair, sends each request line,
// and returns the decoded responses in order.
func drive(t *testing.T, requests ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	s := New("test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var responses []map[string]any
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad response line %q: %v", sc.Text(), err)
		}
		responses = append(responses, m)
	}
	return responses
}

// toolCallResult extracts and decodes the JSON text payload of a tools/call response.
func toolCallResult(t *testing.T, resp map[string]any) (map[string]any, bool) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool payload not JSON: %q", text)
	}
	return payload, result["isError"] == true
}

func TestMCPHandshakeAndToolsList(t *testing.T) {
	resps := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope/nope"}`,
	)
	if len(resps) != 3 {
		t.Fatalf("want 3 responses (notification unanswered), got %d: %v", len(resps), resps)
	}
	init := resps[0]["result"].(map[string]any)
	if init["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}
	if init["serverInfo"].(map[string]any)["name"] != "agora-qa" {
		t.Errorf("serverInfo.name = %v", init["serverInfo"])
	}
	tools := resps[1]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"detect_tests", "run_tests", "run_case_script", "write_test_file"} {
		if !names[want] {
			t.Errorf("tools/list missing %q (got %v)", want, names)
		}
	}
	// Unknown method with an id → JSON-RPC error, not silence.
	if resps[2]["error"] == nil {
		t.Errorf("unknown method must error, got %v", resps[2])
	}
}

func TestDetectAndRunTests_GoRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmp\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resps := drive(t, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"detect_tests","arguments":{"dir":%q}}}`, dir))
	payload, isErr := toolCallResult(t, resps[0])
	if isErr {
		t.Fatalf("detect_tests errored: %v", payload)
	}
	if payload["command"] != "go test ./..." || payload["framework"] != "go" {
		t.Errorf("detect = %v, want go test ./.../go", payload)
	}
}

func TestRunTests_RealExitCodes(t *testing.T) {
	dir := t.TempDir()
	// Pass: exit 0. Fail: exit 3. The tool must report the REAL code.
	resps := drive(t,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_tests","arguments":{"dir":%q,"command":"echo all green"}}}`, dir),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_tests","arguments":{"dir":%q,"command":"exit 3"}}}`, dir),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_tests","arguments":{"dir":%q}}}`, dir),
	)
	pass, _ := toolCallResult(t, resps[0])
	if pass["passed"] != true || pass["exit_code"].(float64) != 0 || !strings.Contains(pass["output"].(string), "all green") {
		t.Errorf("pass run = %v", pass)
	}
	fail, _ := toolCallResult(t, resps[1])
	if fail["passed"] != false || fail["exit_code"].(float64) != 3 {
		t.Errorf("fail run must report exit 3, got %v", fail)
	}
	// No command detectable in an empty dir → explicit "none configured", not a crash.
	none, _ := toolCallResult(t, resps[2])
	if none["passed"] != false || none["command"] != "" {
		t.Errorf("no-command run = %v", none)
	}
}

func TestRunCaseScript(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	pass := `process.exit(0)`
	fail := `console.error("expected Greet, got Get greeting"); process.exit(1)`
	resps := drive(t,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_case_script","arguments":{"dir":%q,"script":%q}}}`, dir, pass),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_case_script","arguments":{"dir":%q,"script":%q}}}`, dir, fail),
	)
	p, _ := toolCallResult(t, resps[0])
	if p["passed"] != true {
		t.Errorf("pass script = %v", p)
	}
	f, _ := toolCallResult(t, resps[1])
	if f["passed"] != false || !strings.Contains(f["output"].(string), "expected Greet") {
		t.Errorf("fail script must surface stderr, got %v", f)
	}
	// The temp module is cleaned up.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".agora-qa-case-") {
			t.Errorf("temp case script %q not cleaned up", e.Name())
		}
	}
}

func TestWriteTestFileGuards(t *testing.T) {
	dir := t.TempDir()
	call := func(id int, args string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"write_test_file","arguments":%s}}`, id, args)
	}
	resps := drive(t,
		call(1, fmt.Sprintf(`{"dir":%q,"path":"tests/greet.spec.ts","content":"it('x',()=>{})"}`, dir)),
		call(2, fmt.Sprintf(`{"dir":%q,"path":"tests/greet.spec.ts","content":"changed"}`, dir)),                    // exists, no overwrite
		call(3, fmt.Sprintf(`{"dir":%q,"path":"../escape.spec.ts","content":"x"}`, dir)),                            // escape
		call(4, fmt.Sprintf(`{"dir":%q,"path":"src/app.ts","content":"not a test"}`, dir)),                          // not test-looking
		call(5, fmt.Sprintf(`{"dir":%q,"path":"tests/greet.spec.ts","content":"changed","overwrite":true}`, dir)),   // explicit overwrite
	)
	// All five calls ran in one session — assert per-call outcomes, then the
	// FINAL file content (call 5's explicit overwrite wins).
	if p, isErr := toolCallResult(t, resps[0]); isErr {
		t.Fatalf("first write must succeed: %v", p)
	}
	for i, wantErr := range map[int]string{1: "already exists", 2: "relative", 3: "does not look like a test"} {
		if p, isErr := toolCallResult(t, resps[i]); !isErr {
			t.Errorf("call %d must fail (%s), got %v", i+1, wantErr, p)
		}
	}
	if p, isErr := toolCallResult(t, resps[4]); isErr {
		t.Errorf("overwrite=true must succeed: %v", p)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "tests", "greet.spec.ts")); err != nil || string(b) != "changed" {
		t.Errorf("final content = %q (%v), want the overwrite to have won", b, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.spec.ts")); !os.IsNotExist(err) {
		t.Errorf("escape path must not be written outside the repo")
	}
}

// TestRelatedTestCommand pins the A1 selection lever: runner-native related-
// test commands per framework, full-suite fallback when unsupported.
func TestRelatedTestCommand(t *testing.T) {
	// vitest via pnpm script → pnpm exec vitest related
	cmd, ok := relatedTestCommand("pnpm run test", "vitest", []string{"src/greet.ts"})
	if !ok || cmd != "pnpm exec vitest related --run 'src/greet.ts'" {
		t.Errorf("vitest related = %q ok=%v", cmd, ok)
	}
	// jest via npm → npx + passWithNoTests
	cmd, ok = relatedTestCommand("npm run test", "jest", []string{"a.js", "b.js"})
	if !ok || cmd != "npx jest --findRelatedTests 'a.js' 'b.js' --passWithNoTests" {
		t.Errorf("jest related = %q ok=%v", cmd, ok)
	}
	// go: .go files → their packages, non-.go ignored, sorted unique
	cmd, ok = relatedTestCommand("go test ./...", "go", []string{"pkg/b/x.go", "pkg/a/y.go", "pkg/a/z.go", "README.md"})
	if !ok || cmd != "go test './pkg/a' './pkg/b'" {
		t.Errorf("go related = %q ok=%v", cmd, ok)
	}
	// go with only non-.go files → no selection
	if _, ok = relatedTestCommand("go test ./...", "go", []string{"README.md"}); ok {
		t.Error("go related with no .go files must not select")
	}
	// unsupported framework → fallback
	if _, ok = relatedTestCommand("make test", "make", []string{"x.c"}); ok {
		t.Error("unsupported framework must fall back to full suite")
	}
	// empty files → fallback
	if _, ok = relatedTestCommand("pnpm run test", "vitest", []string{" "}); ok {
		t.Error("empty file list must fall back")
	}
}
