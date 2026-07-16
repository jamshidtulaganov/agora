package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Folder browser backing GET /editor/fs/list — the web "Add local folder"
// picker. A browser cannot OS-pick a folder on the DAEMON's machine (and
// <input type="file"> never exposes an absolute path), so the picker walks the
// daemon's filesystem one directory at a time instead of making the human type
// a path.
//
// This surface is READ-ONLY and grants nothing: listing a folder never writes
// the allowlist and never approves anything. Attaching still goes through the
// server's daemon-access gate, and RUNNING an agent in the folder still needs
// the machine owner's task-time consent (checkLocalDirApproved).

// fsBrowseEntry is one directory child in a listing.
type fsBrowseEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	IsGitRepo bool   `json:"is_git_repo"`
	IsSymlink bool   `json:"is_symlink"`
}

// fsBrowseResult is the GET /editor/fs/list response body. Parent is blank at a
// browsable-root boundary so the UI cannot navigate above it.
type fsBrowseResult struct {
	Path      string          `json:"path"`
	Parent    string          `json:"parent"`
	Home      string          `json:"home"`
	Entries   []fsBrowseEntry `json:"entries"`
	Truncated bool            `json:"truncated"`
}

// fsBrowseMaxEntries caps one listing. A directory with tens of thousands of
// children would otherwise build a payload no picker can render.
const fsBrowseMaxEntries = 1000

// browsableRoots returns the roots the picker may walk: the daemon user's home
// plus every owner-approved directory. Approved dirs are included because they
// may live outside home (/srv/code), and the owner already consented to them.
func browsableRoots() (home string, approved []string) {
	if h, err := os.UserHomeDir(); err == nil && strings.TrimSpace(h) != "" {
		home = filepath.Clean(h)
	}
	approved, _ = loadLocalDirAllowlist()
	return home, approved
}

// isUnderRoot reports whether target is root or sits beneath it. Containment is
// separator-boundary aware so /home/user-2 is not treated as inside /home/user.
func isUnderRoot(target, root string) bool {
	if root == "" {
		return false
	}
	t := filepath.Clean(target)
	r := filepath.Clean(root)
	if t == r {
		return true
	}
	return strings.HasPrefix(t, r+string(filepath.Separator))
}

// fsBrowseAllowed applies the two gates a browsable path must pass.
//
// Positive gate (mandatory — the existing blacklist is rejection-only and would
// happily allow /etc/../srv): the path must be inside home or an approved dir.
// Without it, any workspace member reaching this daemon could enumerate the
// owner's whole disk.
//
// Negative gate: even inside home, deny credential/OS stores (~/.ssh, ~/.aws,
// ~/Library, AppData) and system roots. Note $HOME ITSELF is allowed here —
// validateLocalPath bans it as an EXECUTION target, but it is exactly the
// default browse root, so the "== home" rejection must not apply to listing.
func fsBrowseAllowed(realPath, home string, approved []string) error {
	real := filepath.Clean(realPath)
	// Compare against home in BOTH its literal and symlink-resolved forms: the
	// candidate arrives resolved, and a home behind a link (macOS /var →
	// /private/var, or a relocated profile) would otherwise never contain it.
	// isLocalDirApproved already resolves its own entries.
	if !isUnderRoot(real, home) && !isUnderRoot(real, evalSymlinksOr(home)) && !isLocalDirApproved(real, approved) {
		return fmt.Errorf("path is outside the browsable roots (the daemon user's home directory or an approved folder): %q", real)
	}
	if isDriveRoot(real) {
		return fmt.Errorf("path is a drive root %q", real)
	}
	for _, banned := range systemRootBlacklist() {
		if real == filepath.Clean(banned) {
			return fmt.Errorf("path is a protected system root %q", banned)
		}
		if r, err := filepath.EvalSymlinks(banned); err == nil && filepath.Clean(r) == real {
			return fmt.Errorf("path is a protected system root %q", banned)
		}
	}
	if home != "" {
		if reason, blocked := isProtectedHomeSubtree(real, home); blocked {
			return errors.New(reason)
		}
		if reason, blocked := isProtectedHomeSubtree(real, evalSymlinksOr(home)); blocked {
			return errors.New(reason)
		}
	}
	return nil
}

// fsBrowseChildListable reports whether a child of an already-allowed directory
// should appear in the listing. The positive root gate is inherited from the
// parent, so only the protected-home classes (~/Library, dot-dirs under $HOME)
// can still be refused — and those are a pure string check, deliberately: the
// full fsBrowseAllowed resolves symlinks and would cost several stats per entry
// on a directory with hundreds of children. A symlinked child pointing at a
// banned target still fails closed when the user actually opens it, because
// browseLocalDir re-gates on the resolved path.
func fsBrowseChildListable(child, home string) bool {
	if home == "" {
		return true
	}
	if _, blocked := isProtectedHomeSubtree(filepath.Clean(child), home); blocked {
		return false
	}
	if _, blocked := isProtectedHomeSubtree(filepath.Clean(child), evalSymlinksOr(home)); blocked {
		return false
	}
	return true
}

// evalSymlinksOr returns path's symlink-resolved form, or the cleaned path when
// it cannot be resolved (missing/broken link) so callers can still compare.
func evalSymlinksOr(path string) string {
	if path == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(path)
}

// browseLocalDir lists the immediate DIRECTORY children of reqPath. An empty
// reqPath defaults to the daemon user's home (the picker's landing view).
// Returns the HTTP status the handler should use on error.
func browseLocalDir(reqPath string, includeHidden bool) (*fsBrowseResult, int, error) {
	home, approved := browsableRoots()

	target := strings.TrimSpace(reqPath)
	if target == "" {
		// A headless daemon may have no resolvable home; fall back to the first
		// approved dir so allowlist-only boxes are still browsable.
		if home != "" {
			target = home
		} else if len(approved) > 0 {
			target = approved[0]
		} else {
			return nil, http.StatusBadRequest, errors.New("no home directory and no approved folders to browse on this machine")
		}
	}

	abs, err := normalizeLocalPath(target)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	// Resolve symlinks before gating so a link out of home cannot smuggle the
	// walk somewhere the gates would reject.
	real, _ := resolveRealPath(abs)
	real = filepath.Clean(real)
	if err := fsBrowseAllowed(real, home, approved); err != nil {
		return nil, http.StatusForbidden, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, http.StatusNotFound, fmt.Errorf("path does not exist: %q", abs)
		}
		return nil, http.StatusForbidden, fmt.Errorf("cannot read %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, http.StatusBadRequest, fmt.Errorf("path is not a directory: %q", abs)
	}

	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, http.StatusForbidden, fmt.Errorf("cannot list %q: %w", abs, err)
	}

	entries := make([]fsBrowseEntry, 0, len(dirents))
	truncated := false
	for _, de := range dirents {
		name := de.Name()
		if !includeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		isSymlink := de.Type()&os.ModeSymlink != 0
		isDir := de.IsDir()
		if isSymlink {
			// Stat resolves ONE level to learn whether the link points at a
			// directory. Never recurse through links — that is how a browser
			// hits a symlink loop.
			st, serr := os.Stat(filepath.Join(abs, name))
			if serr != nil {
				continue // dangling or unreadable link target
			}
			isDir = st.IsDir()
		}
		if !isDir {
			continue // dirs only; the picker attaches folders, not files
		}
		child := filepath.Join(abs, name)
		if !fsBrowseChildListable(child, home) {
			// Don't advertise a folder the gates would refuse to open (~/Library,
			// dot-dirs under $HOME when hidden are shown). Listing it would only
			// hand the user a row that 403s when clicked.
			continue
		}
		if len(entries) >= fsBrowseMaxEntries {
			truncated = true
			break
		}
		entries = append(entries, fsBrowseEntry{
			Name:      name,
			Path:      child,
			IsDir:     true,
			IsGitRepo: isGitRepoDir(child),
			IsSymlink: isSymlink,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	return &fsBrowseResult{
		Path:      abs,
		Parent:    browsableParent(abs, home, approved),
		Home:      home,
		Entries:   entries,
		Truncated: truncated,
	}, http.StatusOK, nil
}

// browsableParent returns the parent directory to offer as "up one level", or
// "" at a root boundary. Returning filepath.Dir unconditionally would let the
// UI walk from ~/proj up to /Users and out of the browsable roots.
func browsableParent(abs, home string, approved []string) string {
	parent := filepath.Dir(abs)
	if parent == abs {
		return "" // filesystem root
	}
	real, _ := resolveRealPath(parent)
	if fsBrowseAllowed(filepath.Clean(real), home, approved) != nil {
		return ""
	}
	return parent
}

// isGitRepoDir reports whether dir holds a .git entry (dir for a normal clone,
// file for a worktree/submodule). A cheap stat per child — `git rev-parse`
// would fork a process for every entry in the listing.
func isGitRepoDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
