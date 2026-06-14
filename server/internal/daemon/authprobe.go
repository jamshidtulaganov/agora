package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// AuthInfo describes the login / "account" state of a single agent CLI as
// observed on this machine. It is the unit the daemon attaches to each
// registered runtime's metadata so the web UI can render a per-provider
// "connected as <email> (<plan>)" status instead of asking users to paste
// raw API keys.
//
// The three fields map 1:1 onto the runtime metadata keys persisted by the
// server (auth_state / account_email / account_plan).
type AuthInfo struct {
	// AuthState is one of:
	//   "logged_in"  — the CLI is authenticated with a subscription/account
	//                  token (NOT a bare API key), ready to run.
	//   "logged_out" — the CLI is installed but has no usable session; the
	//                  user must run the provider's login flow.
	//   "unknown"    — we could not determine the state (no auth subcommand,
	//                  probe timed out, or output was unrecognised). The UI
	//                  treats this the same as "not connected" but with softer
	//                  wording, since absence of proof is not proof of absence.
	AuthState string
	// AccountEmail is the signed-in account's email when the CLI surfaces it.
	// Empty when unknown or when the provider doesn't expose it.
	AccountEmail string
	// AccountPlan is the subscription tier when the CLI surfaces it
	// (e.g. "Pro", "Max", "Team", "Enterprise", "Free"). Empty otherwise.
	AccountPlan string
}

// Auth state constants. Kept as exported strings so the handler and tests can
// reference the exact wire values without copy-pasting literals.
const (
	AuthStateLoggedIn  = "logged_in"
	AuthStateLoggedOut = "logged_out"
	AuthStateUnknown   = "unknown"
)

// authProbeTimeout bounds a single CLI auth probe. Registration must never
// block on a slow or hung auth subcommand — the version probe already gates
// whether a runtime registers at all, and auth state is a best-effort
// enrichment on top of that. A short, hard timeout keeps a misbehaving CLI
// from stalling the whole workspace registration path.
const authProbeTimeout = 4 * time.Second

// ProbeAuth runs the per-provider login/whoami check for one detected CLI and
// maps it to an AuthInfo. It is intentionally best-effort and fast:
//
//   - the command runs under a short, isolated timeout (authProbeTimeout) so a
//     hung CLI cannot stall registerRuntimesForWorkspace;
//   - any failure to even start the probe degrades to AuthStateUnknown rather
//     than propagating an error — a runtime must still register without a
//     readable auth state;
//   - ALL output→state mapping lives in parseAuthOutput (a pure function) so
//     the interesting logic is unit-testable with table tests; ProbeAuth only
//     owns process execution + the filesystem fallback heuristic for Claude.
//
// binPath is the resolved CLI binary path (Config.Agents[provider].Path).
func ProbeAuth(ctx context.Context, provider, binPath string) AuthInfo {
	provider = strings.TrimSpace(strings.ToLower(provider))

	args, ok := authProbeCommand(provider)
	if !ok {
		// No known auth subcommand for this provider. For Claude we still have
		// a filesystem fallback (credentials file presence); everything else is
		// genuinely unknown.
		if provider == "claude" {
			return claudeFilesystemFallback(AuthInfo{AuthState: AuthStateUnknown})
		}
		return AuthInfo{AuthState: AuthStateUnknown}
	}

	probeCtx, cancel := context.WithTimeout(ctx, authProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, binPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	info := parseAuthOutput(provider, stdout.String(), stderr.String(), runErr)

	// Claude has no definitive auth subcommand across versions, so the parser
	// often returns unknown. Supplement with a filesystem signal: presence of
	// a credentials file means a token was stored at some point. See
	// claudeFilesystemFallback for the documented heuristic.
	if provider == "claude" && info.AuthState == AuthStateUnknown {
		info = claudeFilesystemFallback(info)
	}

	return info
}

// authProbeCommand returns the argv (after the binary) used to probe a
// provider's auth state, and whether a probe command is known at all.
//
// The commands are deliberately read-only status checks, never anything that
// could mutate state or open a browser:
//
//   - codex:  `codex login status`  — prints "Logged in" + account email or
//             "Not logged in".
//   - gemini: `gemini auth status`  — prints the active account / "not
//             authenticated". (Older builds may not support it; an exec error
//             then maps to unknown, which is acceptable.)
//   - claude: no stable, non-interactive auth subcommand exists across Claude
//             Code versions, so we report (false) here and rely on the
//             filesystem fallback in ProbeAuth.
//   - antigravity: the `agy` CLI exposes no auth/status subcommand (it's print-
//             mode only), so it is genuinely unknown.
func authProbeCommand(provider string) ([]string, bool) {
	switch provider {
	case "codex":
		return []string{"login", "status"}, true
	case "gemini":
		return []string{"auth", "status"}, true
	default:
		// claude (filesystem fallback), antigravity (no probe), and any other
		// provider fall through to unknown.
		return nil, false
	}
}

var (
	// emailRe extracts the first email-shaped token from CLI output.
	emailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	// planRe matches a "Plan: <tier>" / "Subscription: <tier>" style line and
	// captures the tier word. Case-insensitive; the captured value is title-
	// cased by normalizePlan.
	planRe = regexp.MustCompile(`(?i)\b(?:plan|subscription|tier)\b\s*[:=]?\s*([A-Za-z][A-Za-z0-9 +/_-]{0,30})`)
	// bareePlanRe matches a known subscription tier appearing on its own
	// (e.g. Claude printing "Claude Max" or "Pro"), used as a fallback when
	// there is no explicit "Plan:" label.
	barePlanRe = regexp.MustCompile(`(?i)\b(claude\s+max|claude\s+pro|max|pro|team|enterprise|business|free|ultra)\b`)
)

// loggedOutMarkers are substrings that, when present in stdout/stderr,
// definitively indicate the CLI has no usable session. Matched
// case-insensitively against the combined output.
var loggedOutMarkers = []string{
	"not logged in",
	"not authenticated",
	"please log in",
	"please login",
	"please run",
	"run `claude setup-token`",
	"setup-token",
	"no credentials",
	"no api key",
	"invalid api key",
	"login required",
	"you are not signed in",
	"session has expired",
	"unauthenticated",
	"logged out",
}

// loggedInMarkers are substrings that indicate an active session. Matched
// case-insensitively. Email/plan extraction runs regardless; these just drive
// the AuthState classification when no logged-out marker is present.
var loggedInMarkers = []string{
	"logged in",
	"signed in",
	"authenticated as",
	"you are authenticated",
	"account:",
	"logged in as",
	"active account",
}

// parseAuthOutput is the pure, table-tested core of the auth probe. It maps a
// CLI's captured (stdout, stderr, exitErr) to an AuthInfo for the given
// provider, with AuthState ∈ {logged_in, logged_out, unknown}.
//
// Classification order (provider-agnostic, applied to combined output):
//
//  1. An explicit logged-out marker anywhere → logged_out. This wins over a
//     non-zero exit code's ambiguity AND over a stray email match, because
//     "Not logged in (cached email: a@b.com)" must classify as logged_out.
//  2. An explicit logged-in marker, OR (for providers whose status command
//     prints just an email on success) a lone email with no logged-out marker
//     → logged_in.
//  3. Otherwise unknown. A non-nil exitErr with no recognised marker stays
//     unknown rather than guessing logged_out — many CLIs exit non-zero for
//     reasons unrelated to auth (unknown subcommand on an old build).
//
// Email and plan are extracted opportunistically and attached whenever found,
// but only surfaced for non-logged-out states (a logged-out CLI may echo a
// stale cached email we don't want to present as "connected as").
func parseAuthOutput(provider, stdout, stderr string, exitErr error) AuthInfo {
	provider = strings.TrimSpace(strings.ToLower(provider))
	combined := stdout + "\n" + stderr
	lower := strings.ToLower(combined)

	email := emailRe.FindString(combined)
	plan := extractPlan(combined)

	// 1) Definitive logged-out signal.
	if containsAny(lower, loggedOutMarkers) {
		return AuthInfo{AuthState: AuthStateLoggedOut}
	}

	// 2) Logged-in signals: an explicit marker, or a lone email surfaced by a
	//    status command that succeeded (exitErr == nil). The exitErr guard on
	//    the email-only path avoids treating an error dump that happens to
	//    contain an email (e.g. a support URL) as a live session.
	if containsAny(lower, loggedInMarkers) {
		return AuthInfo{
			AuthState:    AuthStateLoggedIn,
			AccountEmail: email,
			AccountPlan:  plan,
		}
	}
	if email != "" && exitErr == nil {
		return AuthInfo{
			AuthState:    AuthStateLoggedIn,
			AccountEmail: email,
			AccountPlan:  plan,
		}
	}

	// 3) Nothing conclusive.
	return AuthInfo{AuthState: AuthStateUnknown}
}

// extractPlan pulls a subscription tier from CLI output. It prefers an
// explicit "Plan: X" label and falls back to a bare known-tier token. Returns
// "" when no plausible plan is present.
func extractPlan(text string) string {
	if m := planRe.FindStringSubmatch(text); len(m) == 2 {
		if p := normalizePlan(m[1]); p != "" {
			return p
		}
	}
	if m := barePlanRe.FindString(text); m != "" {
		return normalizePlan(m)
	}
	return ""
}

// normalizePlan trims and title-cases a captured plan token, dropping obvious
// non-plan noise. Multi-word values (e.g. "claude max") collapse to a clean
// "Claude Max".
func normalizePlan(raw string) string {
	p := strings.TrimSpace(raw)
	// Cut at the first newline / trailing punctuation the regex may have eaten.
	if i := strings.IndexAny(p, "\n\r.,;)"); i >= 0 {
		p = strings.TrimSpace(p[:i])
	}
	if p == "" {
		return ""
	}
	fields := strings.Fields(p)
	for i, f := range fields {
		lower := strings.ToLower(f)
		// Keep "+" suffixes etc. intact; just upper-case the first rune.
		fields[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(fields, " ")
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// claudeCredentialPaths returns the candidate locations where the Claude Code
// CLI stores its subscription credentials. The exact path has varied across
// installers, so we check the documented ones in priority order.
//
//   - $CLAUDE_CONFIG_DIR/.credentials.json (explicit override)
//   - ~/.claude/.credentials.json          (default config dir)
//   - ~/.config/claude/.credentials.json   (XDG-style layout)
func claudeCredentialPaths() []string {
	var paths []string
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		paths = append(paths, filepath.Join(dir, ".credentials.json"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, ".claude", ".credentials.json"),
			filepath.Join(home, ".config", "claude", ".credentials.json"),
		)
	}
	return paths
}

// claudeFilesystemFallback implements the documented Claude heuristic. The
// Claude Code CLI has no stable, non-interactive "whoami" subcommand we can
// rely on across versions, so when the command-based probe is inconclusive we
// fall back to the strongest available local signal:
//
//	"if we can't find a definitive auth subcommand, treat the presence of a
//	 credentials file as logged_in, else unknown."
//
// A credentials file existing means `claude setup-token` / the OAuth login
// flow stored a subscription token on this machine at some point — a high-
// precision signal that the CLI is connected as an account (not via a bare
// ANTHROPIC_API_KEY). We deliberately do NOT read or parse the file's
// contents: it holds a secret, and presence alone is enough for a status
// badge. Email/plan therefore stay empty on this path (we surface
// "connected", not "connected as …"). When no credentials file exists we
// leave the incoming state (typically unknown) untouched rather than asserting
// logged_out, because an API-key-only setup is a legitimate, separate mode we
// can't see here.
func claudeFilesystemFallback(in AuthInfo) AuthInfo {
	for _, p := range claudeCredentialPaths() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return AuthInfo{
				AuthState:    AuthStateLoggedIn,
				AccountEmail: in.AccountEmail,
				AccountPlan:  in.AccountPlan,
			}
		}
	}
	return in
}
