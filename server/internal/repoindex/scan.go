package repoindex

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// maxFileBytes skips files too large to be worth retrieving. A 1 MB source
	// file is generated, minified, or a data blob — never the thing an agent
	// needs to read to make a change.
	maxFileBytes = 1 << 20
	// maxFiles bounds a pathological repo. Hitting it means the scan is
	// partial; callers surface that rather than silently ranking a subset.
	maxFiles = 20000
	// binarySniffBytes is how much of a file we read to classify it.
	binarySniffBytes = 8192
)

// scanStats reports what one enumeration covered.
type scanStats struct {
	Scanned  int  // files read and fed to the ranker
	Excluded int  // dropped by the floor, the size cap, or binary detection
	Partial  bool // maxFiles hit
	IsGit    bool // enumerated from the git tree rather than a filesystem walk
}

// scanRepo enumerates every indexable file under repoDir and hands each one's
// body to visit. Bodies are streamed, never accumulated — the caller keeps
// only what it needs (see ranker).
//
// Git repos take the fast path: `git ls-tree -r HEAD` yields every tracked
// path in one call, and tracked-ness gives .gitignore semantics for free (an
// ignored file is simply not in the tree, so no gitignore matcher dependency
// is needed). Non-git directories fall back to a filesystem walk.
//
// The exclusion floor applies to both paths — a tracked .env is still denied.
func scanRepo(ctx context.Context, repoDir string, visit func(relPath, lang, body string)) (scanStats, error) {
	if paths, err := gitTreePaths(ctx, repoDir); err == nil {
		stats := scanStats{IsGit: true}
		for _, rel := range paths {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			if stats.Scanned >= maxFiles {
				stats.Partial = true
				break
			}
			if isDeniedPath(rel) {
				stats.Excluded++
				continue
			}
			body, ok := readTextFile(filepath.Join(repoDir, filepath.FromSlash(rel)))
			if !ok {
				stats.Excluded++
				continue
			}
			stats.Scanned++
			visit(rel, langOf(rel), body)
		}
		return stats, nil
	}
	return walkRepo(ctx, repoDir, visit)
}

// gitTreePaths lists HEAD's tracked blob paths. Returns an error for a non-git
// directory or a repo with no commits, which sends the caller to the walk.
func gitTreePaths(ctx context.Context, repoDir string) ([]string, error) {
	// -z: NUL-separated, so paths containing spaces or newlines survive.
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "ls-tree", "-r", "-z", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		// "<mode> <type> <sha>\t<path>"
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(string(rec[:tab]))
		if len(meta) < 2 || meta[1] != "blob" {
			continue // trees and submodule gitlinks
		}
		paths = append(paths, string(rec[tab+1:]))
	}
	if len(paths) == 0 {
		return nil, errNoTree
	}
	return paths, nil
}

// errNoTree marks an empty git tree so scanRepo falls back to walking.
var errNoTree = errors.New("repoindex: empty git tree")

func walkRepo(ctx context.Context, repoDir string, visit func(relPath, lang, body string)) (scanStats, error) {
	stats := scanStats{}
	err := filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, never abort the scan
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path == repoDir {
				return nil
			}
			if isDeniedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if stats.Scanned >= maxFiles {
			stats.Partial = true
			return filepath.SkipAll
		}
		if isDeniedFile(d.Name()) {
			stats.Excluded++
			return nil
		}
		rel, relErr := filepath.Rel(repoDir, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		body, ok := readTextFile(path)
		if !ok {
			stats.Excluded++
			return nil
		}
		stats.Scanned++
		visit(rel, langOf(rel), body)
		return nil
	})
	if err != nil {
		return stats, err
	}
	return stats, nil
}

// readTextFile reads path when it is text and within the size cap. ok=false
// means "not indexable" for any reason — too big, empty, binary, unreadable.
func readTextFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes || info.Size() == 0 {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if isBinary(data) {
		return "", false
	}
	return string(data), true
}

// isBinary classifies by NUL byte in the sniff window — the heuristic git
// itself uses, and it costs one scan of at most 8 KB.
func isBinary(data []byte) bool {
	if len(data) > binarySniffBytes {
		data = data[:binarySniffBytes]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// extLang maps file extension to the language label used for outlining and for
// the pack's fence hints. The set is the languages Agora's own projects use;
// an unlisted extension is still indexed for search, it just has no outline.
var extLang = map[string]string{
	".go":    "go",
	".php":   "php",
	".ts":    "ts",
	".tsx":   "tsx",
	".js":    "js",
	".jsx":   "jsx",
	".vue":   "vue",
	".py":    "python",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".sql":   "sql",
	".rb":    "ruby",
	".java":  "java",
	".rs":    "rust",
	".sh":    "bash",
	".css":   "css",
	".scss":  "scss",
	".html":  "html",
	".json":  "json",
	".yaml":  "yaml",
	".yml":   "yaml",
	".md":    "markdown",
	".mdx":   "markdown",
	".proto": "proto",
}

func langOf(path string) string {
	return extLang[strings.ToLower(filepath.Ext(path))]
}
