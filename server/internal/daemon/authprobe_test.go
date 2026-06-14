package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeExitError is a stand-in for an *exec.ExitError so table tests can drive
// the exitErr branch of parseAuthOutput without spawning a process.
type fakeExitError struct{ code int }

func (e fakeExitError) Error() string { return "exit status" }

func TestParseAuthOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		provider  string
		stdout    string
		stderr    string
		exitErr   error
		wantState string
		wantEmail string
		wantPlan  string
	}{
		// ---- codex --------------------------------------------------------
		{
			name:      "codex logged in with email and plan",
			provider:  "codex",
			stdout:    "Logged in as dev@example.com\nPlan: Pro\n",
			wantState: AuthStateLoggedIn,
			wantEmail: "dev@example.com",
			wantPlan:  "Pro",
		},
		{
			name:      "codex logged in without plan",
			provider:  "codex",
			stdout:    "Logged in as ada@openai.com\n",
			wantState: AuthStateLoggedIn,
			wantEmail: "ada@openai.com",
		},
		{
			name:      "codex not logged in",
			provider:  "codex",
			stdout:    "Not logged in. Run `codex login` to authenticate.\n",
			exitErr:   fakeExitError{code: 1},
			wantState: AuthStateLoggedOut,
		},
		{
			name:      "codex not logged in even with stale cached email",
			provider:  "codex",
			stdout:    "Not logged in (last account: stale@example.com)\n",
			wantState: AuthStateLoggedOut,
			// logged-out must NOT surface the stale email.
			wantEmail: "",
		},

		// ---- gemini -------------------------------------------------------
		{
			name:      "gemini authenticated as email",
			provider:  "gemini",
			stdout:    "Authenticated as user@gmail.com\n",
			wantState: AuthStateLoggedIn,
			wantEmail: "user@gmail.com",
		},
		{
			name:      "gemini lone email on success",
			provider:  "gemini",
			stdout:    "alice@google.com\n",
			wantState: AuthStateLoggedIn,
			wantEmail: "alice@google.com",
		},
		{
			name:      "gemini not authenticated",
			provider:  "gemini",
			stderr:    "Error: not authenticated. Please log in.\n",
			exitErr:   fakeExitError{code: 1},
			wantState: AuthStateLoggedOut,
		},

		// ---- claude (parser-only; filesystem fallback tested separately) --
		{
			name:      "claude logged in marker",
			provider:  "claude",
			stdout:    "Logged in as claude-user@anthropic.com (Claude Max)\n",
			wantState: AuthStateLoggedIn,
			wantEmail: "claude-user@anthropic.com",
			wantPlan:  "Claude Max",
		},
		{
			name:      "claude setup-token hint means logged out",
			provider:  "claude",
			stderr:    "Invalid API key. Run `claude setup-token` to sign in.\n",
			exitErr:   fakeExitError{code: 1},
			wantState: AuthStateLoggedOut,
		},
		{
			name:      "claude unrecognized output is unknown",
			provider:  "claude",
			stdout:    "2.1.5 (Claude Code)\n",
			wantState: AuthStateUnknown,
		},

		// ---- antigravity --------------------------------------------------
		{
			name:      "antigravity has no auth output -> unknown",
			provider:  "antigravity",
			stdout:    "agy 0.4.0\n",
			wantState: AuthStateUnknown,
		},
		{
			name:      "antigravity still detects an explicit logged-out marker",
			provider:  "antigravity",
			stderr:    "You are not signed in.\n",
			wantState: AuthStateLoggedOut,
		},

		// ---- generic edge cases ------------------------------------------
		{
			name:      "email present but command errored -> not logged_in via email path",
			provider:  "gemini",
			stdout:    "See https://support.google.com or contact help@google.com\n",
			exitErr:   fakeExitError{code: 2},
			wantState: AuthStateUnknown,
		},
		{
			name:      "explicit logged-in marker wins even with non-zero exit",
			provider:  "codex",
			stdout:    "Logged in as who@x.io\n",
			exitErr:   fakeExitError{code: 3},
			wantState: AuthStateLoggedIn,
			wantEmail: "who@x.io",
		},
		{
			name:      "plan via Subscription label",
			provider:  "codex",
			stdout:    "Signed in as a@b.com\nSubscription: Team\n",
			wantState: AuthStateLoggedIn,
			wantEmail: "a@b.com",
			wantPlan:  "Team",
		},
		{
			name:      "empty output unknown provider -> unknown",
			provider:  "somethingelse",
			wantState: AuthStateUnknown,
		},
		{
			name:      "logged-out marker beats a lone email",
			provider:  "gemini",
			stdout:    "cached@x.com\nLogin required.\n",
			wantState: AuthStateLoggedOut,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseAuthOutput(tc.provider, tc.stdout, tc.stderr, tc.exitErr)
			if got.AuthState != tc.wantState {
				t.Errorf("AuthState = %q, want %q", got.AuthState, tc.wantState)
			}
			if got.AccountEmail != tc.wantEmail {
				t.Errorf("AccountEmail = %q, want %q", got.AccountEmail, tc.wantEmail)
			}
			if got.AccountPlan != tc.wantPlan {
				t.Errorf("AccountPlan = %q, want %q", got.AccountPlan, tc.wantPlan)
			}
		})
	}
}

func TestParseAuthOutput_LoggedOutNeverLeaksEmailOrPlan(t *testing.T) {
	t.Parallel()
	got := parseAuthOutput("codex", "Not logged in. cached: x@y.com Plan: Pro", "", nil)
	if got.AuthState != AuthStateLoggedOut {
		t.Fatalf("AuthState = %q, want logged_out", got.AuthState)
	}
	if got.AccountEmail != "" || got.AccountPlan != "" {
		t.Fatalf("logged_out leaked email/plan: email=%q plan=%q", got.AccountEmail, got.AccountPlan)
	}
}

func TestExtractPlan(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Plan: Max":                  "Max",
		"plan = pro":                 "Pro",
		"Subscription: Enterprise":   "Enterprise",
		"You are on the Claude Pro.": "Claude Pro",
		"just some text":             "",
		"Tier: business plan":        "Business Plan",
	}
	for in, want := range cases {
		if got := extractPlan(in); got != want {
			t.Errorf("extractPlan(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProbeAuth_UnknownProviderDegradesGracefully verifies the exec wrapper
// never errors out for a provider with no probe command (and no claude
// fallback) — it must return unknown.
func TestProbeAuth_UnknownProviderDegradesGracefully(t *testing.T) {
	t.Parallel()
	got := ProbeAuth(context.Background(), "antigravity", "/nonexistent/agy")
	if got.AuthState != AuthStateUnknown {
		t.Fatalf("AuthState = %q, want unknown", got.AuthState)
	}
}

// TestProbeAuth_CodexBadBinaryIsUnknown verifies that when the probe command
// can't even start (binary missing), we degrade to unknown rather than
// panicking or asserting logged_out.
func TestProbeAuth_CodexBadBinaryIsUnknown(t *testing.T) {
	t.Parallel()
	got := ProbeAuth(context.Background(), "codex", "/nonexistent/codex-binary-xyz")
	if got.AuthState != AuthStateUnknown {
		t.Fatalf("AuthState = %q, want unknown (missing binary)", got.AuthState)
	}
}

// TestClaudeFilesystemFallback_CredentialsFilePresent drives the documented
// Claude heuristic: a credentials file in CLAUDE_CONFIG_DIR means logged_in.
func TestClaudeFilesystemFallback_CredentialsFilePresent(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"token":"redacted"}`), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	got := claudeFilesystemFallback(AuthInfo{AuthState: AuthStateUnknown})
	if got.AuthState != AuthStateLoggedIn {
		t.Fatalf("AuthState = %q, want logged_in (creds file present)", got.AuthState)
	}
	// Presence-only signal: must not fabricate an email/plan.
	if got.AccountEmail != "" || got.AccountPlan != "" {
		t.Fatalf("fallback fabricated email/plan: %+v", got)
	}
}

// TestClaudeFilesystemFallback_NoCredentialsFileKeepsIncoming verifies that
// when no credentials file exists, the incoming state is preserved (we don't
// assert logged_out — an API-key-only setup is a legitimate mode we can't see).
func TestClaudeFilesystemFallback_NoCredentialsFileKeepsIncoming(t *testing.T) {
	dir := t.TempDir() // empty: no .credentials.json
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	// Also point HOME at an empty dir so the default ~/.claude path can't match
	// a real developer credentials file on the machine running the tests.
	t.Setenv("HOME", dir)

	in := AuthInfo{AuthState: AuthStateUnknown}
	got := claudeFilesystemFallback(in)
	if got.AuthState != AuthStateUnknown {
		t.Fatalf("AuthState = %q, want unknown preserved", got.AuthState)
	}
}

// Guard: the fakeExitError must satisfy the error interface so the exitErr
// branch is exercised with a non-nil error value.
var _ error = fakeExitError{}

func TestFakeExitErrorIsError(t *testing.T) {
	var err error = fakeExitError{code: 1}
	if !errors.As(err, &fakeExitError{}) {
		t.Fatal("fakeExitError should satisfy errors.As")
	}
}
