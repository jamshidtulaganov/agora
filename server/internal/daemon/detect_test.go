package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFiles materializes a fixture repo in a temp dir: keys are relative
// paths (subdirs are created), values are file contents.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectDevCommand(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "node with pnpm lockfile",
			files: map[string]string{
				"package.json":   `{"scripts":{"dev":"vite"}}`,
				"pnpm-lock.yaml": "",
			},
			want: "pnpm run dev",
		},
		{
			name: "node beats Makefile",
			files: map[string]string{
				"package.json": `{"scripts":{"dev":"vite"}}`,
				"Makefile":     "dev:\n\techo dev\n",
			},
			want: "npm run dev",
		},
		{
			name: "package.json without dev script falls through to Makefile",
			files: map[string]string{
				"package.json": `{"scripts":{"build":"tsc"}}`,
				"Makefile":     "start:\n\techo start\n",
			},
			want: "make start",
		},
		{
			name: "Makefile with tab recipes",
			files: map[string]string{
				"Makefile": "build:\n\tgo build ./...\n\ndev:\n\tgo run ./cmd/server\n",
			},
			want: "make dev",
		},
		{
			name: "priority order wins over file order",
			files: map[string]string{
				// serve appears first in the file; dev must still win.
				"Makefile": "serve:\n\techo serve\n\ndev:\n\techo dev\n",
			},
			want: "make dev",
		},
		{
			name: "dev variable assignment does not match",
			files: map[string]string{
				"Makefile": "dev := x\n\nbuild:\n\techo build\n",
			},
			want: "",
		},
		{
			name: "dev with prerequisites matches",
			files: map[string]string{
				"Makefile": "dev: build\n\techo dev\n",
			},
			want: "make dev",
		},
		{
			name: "GNUmakefile spelling",
			files: map[string]string{
				"GNUmakefile": "run:\n\techo run\n",
			},
			want: "make run",
		},
		{
			name: "Yii layout composer.json plus web/index.php",
			files: map[string]string{
				"composer.json": `{"require":{"yiisoft/yii2":"*"}}`,
				"web/index.php": "<?php",
			},
			want: "php -S 127.0.0.1:${PORT} -t web",
		},
		{
			name: "public/index.php variant",
			files: map[string]string{
				"composer.json":    `{}`,
				"public/index.php": "<?php",
			},
			want: "php -S 127.0.0.1:${PORT} -t public",
		},
		{
			name: "bare root index.php without composer.json",
			files: map[string]string{
				"index.php": "<?php",
			},
			want: "php -S 127.0.0.1:${PORT}",
		},
		{
			name: "composer.json without any index.php",
			files: map[string]string{
				"composer.json": `{}`,
			},
			want: "",
		},
		{
			name:  "empty dir",
			files: map[string]string{},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFiles(t, tt.files)
			if got := detectDevCommand(dir); got != tt.want {
				t.Errorf("detectDevCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectTestCommand(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "package.json test script wins over everything",
			files: map[string]string{
				"package.json": `{"scripts":{"test":"vitest run"}}`,
				"Makefile":     "test:\n\tgo test ./...\n",
				"go.mod":       "module example.com/x\n",
			},
			want: "npm run test",
		},
		{
			name: "package.json test script with yarn lockfile",
			files: map[string]string{
				"package.json": `{"scripts":{"test":"jest"}}`,
				"yarn.lock":    "",
			},
			want: "yarn run test",
		},
		{
			name: "Makefile test target",
			files: map[string]string{
				"Makefile": "test:\n\tgo test ./...\n",
			},
			want: "make test",
		},
		{
			name: "go.mod",
			files: map[string]string{
				"go.mod": "module example.com/x\n",
			},
			want: "go test ./...",
		},
		{
			name: "composer scripts.test",
			files: map[string]string{
				"composer.json": `{"scripts":{"test":"phpunit"}}`,
			},
			want: "composer test",
		},
		{
			name: "composer scripts.test as array",
			files: map[string]string{
				"composer.json": `{"scripts":{"test":["phpunit","phpstan"]}}`,
			},
			want: "composer test",
		},
		{
			name: "composer without test script",
			files: map[string]string{
				"composer.json": `{"scripts":{"lint":"phpcs"}}`,
			},
			want: "",
		},
		{
			name:  "empty dir",
			files: map[string]string{},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFiles(t, tt.files)
			if got := detectTestCommand(dir); got != tt.want {
				t.Errorf("detectTestCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

// stubDepInstall replaces runDepInstall for the test and records invocations.
func stubDepInstall(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	orig := runDepInstall
	runDepInstall = func(repo, install string) (string, error) {
		calls = append(calls, install)
		return "stub install ok", nil
	}
	t.Cleanup(func() { runDepInstall = orig })
	return &calls
}

func TestEnsureDeps(t *testing.T) {
	t.Run("composer.json without vendor triggers composer install", func(t *testing.T) {
		calls := stubDepInstall(t)
		dir := writeFiles(t, map[string]string{"composer.json": `{}`})
		out, err := ensureDeps(dir)
		if err != nil {
			t.Fatalf("ensureDeps() error = %v", err)
		}
		if out != "stub install ok" {
			t.Errorf("ensureDeps() out = %q", out)
		}
		if len(*calls) != 1 || (*calls)[0] != "composer install --no-interaction" {
			t.Errorf("runDepInstall calls = %v, want [composer install --no-interaction]", *calls)
		}
	})

	t.Run("populated node_modules is a no-op", func(t *testing.T) {
		calls := stubDepInstall(t)
		dir := writeFiles(t, map[string]string{
			"package.json":             `{"scripts":{"dev":"vite"}}`,
			"node_modules/.package-ok": "",
		})
		out, err := ensureDeps(dir)
		if err != nil {
			t.Fatalf("ensureDeps() error = %v", err)
		}
		if out != "" || len(*calls) != 0 {
			t.Errorf("ensureDeps() = %q, calls = %v; want no-op", out, *calls)
		}
	})

	t.Run("provider-less repo is a no-op", func(t *testing.T) {
		calls := stubDepInstall(t)
		dir := writeFiles(t, map[string]string{"go.mod": "module example.com/x\n"})
		out, err := ensureDeps(dir)
		if err != nil {
			t.Fatalf("ensureDeps() error = %v", err)
		}
		if out != "" || len(*calls) != 0 {
			t.Errorf("ensureDeps() = %q, calls = %v; want no-op", out, *calls)
		}
	})

	t.Run("node repo without node_modules installs via lockfile pm", func(t *testing.T) {
		calls := stubDepInstall(t)
		dir := writeFiles(t, map[string]string{
			"package.json":   `{"scripts":{"dev":"vite"}}`,
			"pnpm-lock.yaml": "",
		})
		if _, err := ensureDeps(dir); err != nil {
			t.Fatalf("ensureDeps() error = %v", err)
		}
		if len(*calls) != 1 || (*calls)[0] != "pnpm install" {
			t.Errorf("runDepInstall calls = %v, want [pnpm install]", *calls)
		}
	})
}
