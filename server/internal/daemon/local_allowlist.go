package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// The local-directory allowlist is the machine owner's consent record for
// local_directory execution: the daemon refuses to run a task inside a
// folder unless that folder (or an ancestor) appears here. The server-side
// project_resource row is deliberately NOT authoritative — anything writable
// through the API is forfeit to a leaked credential, so consent lives on the
// executing machine only.
//
// Entries come from two additive sources:
//
//   - ~/.agora/local-dirs.json — written by the desktop folder picker (the
//     pick IS the consent gesture) and by `agora daemon allow-dir <path>`.
//     Machine-wide like daemon.id, not per-profile: consent belongs to the
//     OS user, not to an Agora profile.
//   - AGORA_LOCAL_DIR_ALLOWLIST — os.PathListSeparator-separated absolute
//     paths, for headless/containerized daemons where no picker or CLI
//     round-trip exists.
//
// The file is re-read on every task so approvals take effect without a
// daemon restart.
const localDirsFileName = "local-dirs.json"

type localDirAllowlistFile struct {
	Version int      `json:"version"`
	Dirs    []string `json:"dirs"`
}

// localDirAllowlistPath returns ~/.agora/local-dirs.json.
func localDirAllowlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".agora", localDirsFileName), nil
}

// loadLocalDirAllowlist returns the merged approval entries. A missing file
// is an empty list, not an error; a corrupt file IS an error so the caller
// can log it — but the returned slice still carries the env entries, keeping
// the headless escape hatch alive even when the file is damaged.
func loadLocalDirAllowlist() ([]string, error) {
	var entries []string
	var loadErr error

	path, err := localDirAllowlistPath()
	if err != nil {
		loadErr = err
	} else if data, readErr := os.ReadFile(path); readErr == nil {
		var f localDirAllowlistFile
		if jsonErr := json.Unmarshal(data, &f); jsonErr != nil {
			loadErr = fmt.Errorf("parse %s: %w", path, jsonErr)
		} else {
			entries = append(entries, f.Dirs...)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		loadErr = fmt.Errorf("read %s: %w", path, readErr)
	}

	if env := os.Getenv("AGORA_LOCAL_DIR_ALLOWLIST"); env != "" {
		for _, p := range filepath.SplitList(env) {
			if p = strings.TrimSpace(p); p != "" {
				entries = append(entries, p)
			}
		}
	}
	return entries, loadErr
}

// isLocalDirApproved reports whether realPath (the symlink-resolved task
// directory) is equal to or contained in any approved entry. Entries are
// cleaned and symlink-resolved best-effort before comparison so an approval
// recorded as /tmp/proj still matches a task path of /private/tmp/proj.
// Containment is separator-boundary aware: approving /a/b does not approve
// /a/bc. On Windows the comparison is case-insensitive, matching the
// filesystem's semantics.
func isLocalDirApproved(realPath string, entries []string) bool {
	target := filepath.Clean(realPath)
	if runtime.GOOS == "windows" {
		target = strings.ToLower(target)
	}
	for _, entry := range entries {
		e := filepath.Clean(entry)
		if r, err := filepath.EvalSymlinks(e); err == nil {
			e = filepath.Clean(r)
		}
		if runtime.GOOS == "windows" {
			e = strings.ToLower(e)
		}
		if target == e {
			return true
		}
		if strings.HasPrefix(target, e+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// checkLocalDirApproved is the task-time gate: nil when approved, otherwise
// a user-facing error that names both approval paths. absPath is the path as
// the resource recorded it (shown to the user); realPath is the resolved
// form the comparison runs on.
func checkLocalDirApproved(absPath, realPath string) error {
	entries, loadErr := loadLocalDirAllowlist()
	if isLocalDirApproved(realPath, entries) {
		return nil
	}
	msg := fmt.Sprintf(
		"local_directory: %q is not approved for agent execution on this machine. "+
			"The machine owner must approve it first: run `agora daemon allow-dir %s` on the daemon host, "+
			"or re-pick the folder in the Agora desktop app (picking approves it automatically). "+
			"Headless daemons can also set AGORA_LOCAL_DIR_ALLOWLIST.",
		absPath, absPath,
	)
	if loadErr != nil {
		msg = fmt.Sprintf("%s (note: allowlist load problem: %v)", msg, loadErr)
	}
	return errors.New(msg)
}

// ApproveLocalDir records consent for path in ~/.agora/local-dirs.json.
// Used by `agora daemon allow-dir`. The path must be absolute and must not
// be a directory the daemon would refuse anyway (system roots, $HOME itself,
// protected home subtrees like ~/.ssh) — approving those would only produce
// a confusing "approved but still refused" state. The file write is atomic
// (temp + rename) with entries deduped and sorted for stable diffs.
// Returns added=false when the path was already present.
func ApproveLocalDir(path string) (added bool, file string, err error) {
	if !filepath.IsAbs(path) {
		return false, "", fmt.Errorf("path must be absolute, got %q", path)
	}
	cleaned := filepath.Clean(path)
	if reason, blocked := isBlacklistedLocalPath(cleaned); blocked {
		return false, "", fmt.Errorf("refusing to approve: %s", reason)
	}
	if real, evalErr := filepath.EvalSymlinks(cleaned); evalErr == nil {
		if reason, blocked := isBlacklistedRealPath(filepath.Clean(real)); blocked {
			return false, "", fmt.Errorf("refusing to approve: %s", reason)
		}
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return false, "", fmt.Errorf("stat %q: %w", cleaned, err)
	}
	if !info.IsDir() {
		return false, "", fmt.Errorf("%q is not a directory", cleaned)
	}

	file, err = localDirAllowlistPath()
	if err != nil {
		return false, "", err
	}
	var f localDirAllowlistFile
	if data, readErr := os.ReadFile(file); readErr == nil {
		// Tolerate a corrupt file: allow-dir is the repair tool, so it
		// rewrites a fresh file rather than refusing to run.
		_ = json.Unmarshal(data, &f)
	}
	for _, existing := range f.Dirs {
		if filepath.Clean(existing) == cleaned {
			return false, file, nil
		}
	}
	f.Version = 1
	f.Dirs = append(f.Dirs, cleaned)
	sort.Strings(f.Dirs)

	if err := writeAllowlistFileAtomic(file, f); err != nil {
		return false, "", err
	}
	return true, file, nil
}

// RevokeLocalDir removes path from the on-disk allowlist. Used by
// `agora daemon revoke-dir`. Returns removed=false when the path was not
// present (env-var approvals are not touched — they are owner-controlled at
// the process level, not persisted here). Comparison is on the cleaned path.
func RevokeLocalDir(path string) (removed bool, file string, err error) {
	if !filepath.IsAbs(path) {
		return false, "", fmt.Errorf("path must be absolute, got %q", path)
	}
	cleaned := filepath.Clean(path)
	file, err = localDirAllowlistPath()
	if err != nil {
		return false, "", err
	}
	var f localDirAllowlistFile
	if data, readErr := os.ReadFile(file); readErr == nil {
		if jsonErr := json.Unmarshal(data, &f); jsonErr != nil {
			return false, "", fmt.Errorf("parse %s: %w", file, jsonErr)
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		return false, file, nil
	} else {
		return false, "", fmt.Errorf("read %s: %w", file, readErr)
	}

	kept := f.Dirs[:0]
	for _, existing := range f.Dirs {
		if filepath.Clean(existing) == cleaned {
			removed = true
			continue
		}
		kept = append(kept, existing)
	}
	if !removed {
		return false, file, nil
	}
	f.Version = 1
	f.Dirs = kept
	if err := writeAllowlistFileAtomic(file, f); err != nil {
		return false, "", err
	}
	return true, file, nil
}

// ListLocalDirs returns the approvals currently in effect, split by source so
// the CLI can show where each came from. envDirs come from
// AGORA_LOCAL_DIR_ALLOWLIST and are not persisted in the file.
func ListLocalDirs() (fileDirs, envDirs []string, file string, err error) {
	file, err = localDirAllowlistPath()
	if err != nil {
		return nil, nil, "", err
	}
	if data, readErr := os.ReadFile(file); readErr == nil {
		var f localDirAllowlistFile
		if jsonErr := json.Unmarshal(data, &f); jsonErr != nil {
			return nil, nil, file, fmt.Errorf("parse %s: %w", file, jsonErr)
		}
		fileDirs = f.Dirs
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, nil, file, fmt.Errorf("read %s: %w", file, readErr)
	}
	if env := os.Getenv("AGORA_LOCAL_DIR_ALLOWLIST"); env != "" {
		for _, p := range filepath.SplitList(env) {
			if p = strings.TrimSpace(p); p != "" {
				envDirs = append(envDirs, p)
			}
		}
	}
	return fileDirs, envDirs, file, nil
}

// writeAllowlistFileAtomic writes f to the allowlist path via temp + rename so
// a concurrent daemon/CLI read never sees a torn file.
func writeAllowlistFileAtomic(file string, f localDirAllowlistFile) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(file), err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".local-dirs-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, file); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
