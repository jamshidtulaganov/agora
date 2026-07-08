package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

// Dev-server / test-command detection for /editor/preview and /editor/test.
// Tier order is deliberate and must not be reordered: Node first (the common
// vibecoder web stack), then Makefile, then PHP. Hybrid repos (e.g. Laravel
// with both package.json and composer.json) detect Node — the project setting
// qa_smoke_cmd is the escape hatch, not a tier swap.

// nodePackageManager picks the Node package manager from the repo's lockfile
// (pnpm > yarn > npm), the same ordering detectSprintDepProvider uses.
func nodePackageManager(repoDir string) string {
	if fileExists(filepath.Join(repoDir, "pnpm-lock.yaml")) {
		return "pnpm"
	}
	if fileExists(filepath.Join(repoDir, "yarn.lock")) {
		return "yarn"
	}
	return "npm"
}

// detectDevCommand returns a best-guess dev-server command, or "".
// Tiers: package.json scripts → Makefile dev/start/run/serve target →
// PHP built-in server (composer.json / index.php).
func detectDevCommand(repoDir string) string {
	if cmd := detectNodeDevCommand(repoDir); cmd != "" {
		return cmd
	}
	if target := makefileTarget(repoDir, "dev", "start", "run", "serve"); target != "" {
		return "make " + target
	}
	return detectPHPDevCommand(repoDir)
}

// detectNodeDevCommand covers the Node ecosystem via package.json + lockfile.
func detectNodeDevCommand(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	script := ""
	for _, cand := range []string{"dev", "start", "serve"} {
		if _, ok := pkg.Scripts[cand]; ok {
			script = cand
			break
		}
	}
	if script == "" {
		return ""
	}
	return nodePackageManager(repoDir) + " run " + script
}

// detectPHPDevCommand serves a PHP app with PHP's built-in dev server. The
// docroot is probed web/index.php (Yii2) → public/index.php (Laravel/Symfony)
// → root index.php (-t omitted). ${PORT} stays a literal here on purpose:
// startPreview exports PORT into the login shell, which interpolates it, so
// the server binds the daemon-allocated hint port.
func detectPHPDevCommand(repoDir string) string {
	hasComposer := fileExists(filepath.Join(repoDir, "composer.json"))
	hasRootIndex := fileExists(filepath.Join(repoDir, "index.php"))
	if !hasComposer && !hasRootIndex {
		return ""
	}
	for _, docroot := range []string{"web", "public"} {
		if fileExists(filepath.Join(repoDir, docroot, "index.php")) {
			return "php -S 127.0.0.1:${PORT} -t " + docroot
		}
	}
	if hasRootIndex {
		return "php -S 127.0.0.1:${PORT}"
	}
	return ""
}

// detectTestCommand resolves the project's test command. Returns "" when
// nothing is detected (the QA pane then shows "no tests configured" instead of
// failing). CI=1 in runProjectTests forces watch-mode runners to exit with a
// verdict. Tiers: package.json scripts.test → Makefile test target →
// go.mod → composer.json scripts.test.
func detectTestCommand(repoDir string) string {
	if cmd := detectNodeTestCommand(repoDir); cmd != "" {
		return cmd
	}
	if makefileTarget(repoDir, "test") != "" {
		return "make test"
	}
	if fileExists(filepath.Join(repoDir, "go.mod")) {
		return "go test ./..."
	}
	if composerHasScript(repoDir, "test") {
		return "composer test"
	}
	return ""
}

// detectNodeTestCommand resolves the test command from package.json's "test"
// script, run with the lockfile-picked package manager.
func detectNodeTestCommand(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	if _, ok := pkg.Scripts["test"]; !ok {
		return ""
	}
	return nodePackageManager(repoDir) + " run test"
}

// makefileTarget returns the first of the given targets — in the given
// priority order, not file order — defined in the repo's make file
// (Makefile, makefile, or GNUmakefile; first existing wins), or "". Target
// lines are matched with a line-anchored `^target:` regex whose next char must
// not be `=`, so `dev := x` assignments don't match while the `dev: build`
// prerequisite form does. (Not `make -pRrq` database parsing — heavy and
// make-version-sensitive.)
func makefileTarget(repoDir string, targets ...string) string {
	var content string
	found := false
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		if b, err := os.ReadFile(filepath.Join(repoDir, name)); err == nil {
			content = string(b)
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	for _, target := range targets {
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:([^=]|$)`)
		if re.MatchString(content) {
			return target
		}
	}
	return ""
}

// composerHasScript reports whether composer.json defines the named script.
// Composer scripts can be a string or an array — RawMessage accepts both.
func composerHasScript(repoDir, name string) bool {
	data, err := os.ReadFile(filepath.Join(repoDir, "composer.json"))
	if err != nil {
		return false
	}
	var c struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if json.Unmarshal(data, &c) != nil {
		return false
	}
	_, ok := c.Scripts[name]
	return ok
}
