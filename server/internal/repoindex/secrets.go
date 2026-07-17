package repoindex

import (
	"path/filepath"
	"strings"
)

// The exclusion floor is default-deny and NOT overridable. Everything the
// indexer reads can end up pasted into an agent's prompt and shipped to a
// closed model provider, so a file that merely *might* hold a credential is
// worth more excluded than indexed. `.agoraignore` may extend this floor; it
// can never disable it.
//
// The floor is applied to every directory regardless of git status: a repo
// that forgot to .gitignore its .env is exactly the repo that needs this.

// deniedDirNames are skipped wholesale, with their entire subtree. Dot-dirs
// are denied generically by isDeniedDir (.git, .ssh, .aws ...); this list
// covers the non-dot ones that are pure noise or pure risk.
var deniedDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	"out":          true,
	"coverage":     true,
	"__pycache__":  true,
	"bin":          true,
	"obj":          true,
	"secrets":      true,
	"credentials":  true,
}

// deniedNameFragments deny any path whose base name contains one of these,
// case-insensitively. Deliberately broad — "token.go" losing indexing is a
// far cheaper mistake than "token.txt" being indexed.
var deniedNameFragments = []string{
	"secret",
	"credential",
	"password",
	"passwd",
	"apikey",
	"api_key",
	"private_key",
	"privatekey",
	"id_rsa",
	"id_ecdsa",
	"id_ed25519",
	".htpasswd",
}

// deniedExtensions deny by extension regardless of name.
var deniedExtensions = map[string]bool{
	".pem":             true,
	".key":             true,
	".p12":             true,
	".pfx":             true,
	".jks":             true,
	".keystore":        true,
	".crt":             true,
	".cer":             true,
	".der":             true,
	".asc":             true,
	".gpg":             true,
	".pgp":             true,
	".kdbx":            true,
	".ppk":             true,
	".mobileprovision": true,
}

// isDeniedDir reports whether a directory (and its subtree) must be skipped.
// Every dot-dir is denied: .git holds the full history, .ssh/.aws/.gnupg hold
// the account, and the rest is tooling state with no retrieval value.
func isDeniedDir(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return deniedDirNames[strings.ToLower(name)]
}

// isDeniedFile reports whether a file must never be indexed. name is the base
// name only — callers walk directories through isDeniedDir separately.
func isDeniedFile(name string) bool {
	lower := strings.ToLower(name)

	// Dotfiles as a class: .env, .npmrc, .netrc, .pypirc all carry tokens,
	// and no dotfile carries enough retrieval value to be worth triaging.
	if strings.HasPrefix(lower, ".") {
		return true
	}
	// `.env` in any suffixed form that survived the dotfile check
	// (config.env, settings.env.local).
	if lower == "env" || strings.Contains(lower, ".env.") || strings.HasSuffix(lower, ".env") {
		return true
	}
	if deniedExtensions[filepath.Ext(lower)] {
		return true
	}
	for _, frag := range deniedNameFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	// `id_*` keypairs whose exact names aren't in the fragment list
	// (id_dsa, id_rsa.pub and friends).
	if strings.HasPrefix(lower, "id_") {
		return true
	}
	return false
}

// isDeniedPath applies the floor to a full repo-relative path: any denied
// path segment poisons the whole path.
func isDeniedPath(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	parts := strings.Split(relPath, "/")
	for i, part := range parts {
		if i == len(parts)-1 {
			if isDeniedFile(part) {
				return true
			}
			continue
		}
		if isDeniedDir(part) {
			return true
		}
	}
	return false
}
