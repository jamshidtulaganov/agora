// Package imports holds the source-agnostic import framework: one pipeline
// (fetch → normalize → map → dry-run diff → apply → link back) that every
// source adapter — Linear first, then Jira — plugs into.
//
// This file is the credential half of the foundation. The token that reads a
// customer's Linear or Jira workspace is a secret, and the repo already has
// exactly one right way to hold one: seal it with secretbox (AES-256-GCM)
// under a dedicated registry key, decrypt it server-side only, and fail closed
// when the key is unset rather than storing plaintext (git_credential,
// migration 132; zoho_connection, migration 142).
//
// Three properties this file is responsible for, all of them stated as rules
// in docs/importers-plan.md §3.8:
//
//   - The plaintext token is never returned by an endpoint, never logged,
//     never put in an event payload. The data layer helps: the connection
//     listing queries do not select secret_encrypted at all, so a response
//     built from them cannot leak one by accident.
//   - The token never reaches the assistant. Its import tools take a
//     connection_id and nothing else — the chat transcript is persisted, so a
//     secret pasted into it outlives the conversation.
//   - Without AGORA_IMPORT_SECRET_KEY there is no degraded mode. Callers
//     surface ErrSealKeyUnset as a 503; storing the token in the clear
//     "for now" is not one of the options.
package imports

import (
	"errors"
	"fmt"

	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

// SecretKeyEnv is the registry key (config.KindSecret) holding the
// base64-encoded 32-byte master key that seals import connection tokens.
const SecretKeyEnv = "AGORA_IMPORT_SECRET_KEY"

// ErrSealKeyUnset is returned by SecretBox when AGORA_IMPORT_SECRET_KEY is
// missing or malformed. Callers translate it to 503 — never to "store it in
// the clear". Test for it with errors.Is.
var ErrSealKeyUnset = errors.New("imports: AGORA_IMPORT_SECRET_KEY is not configured")

// SecretBox builds the secretbox for import credentials from the environment.
//
// Unlike gitCredentialBox/mcpCredentialBox this deliberately does NOT cache
// the box in a sync.Once. Those cache a failure for the life of the process
// and need a private reset hook so their tests can vary the key; here the
// construction cost (an AES key schedule) is paid at most once per connection
// write or per import job, never per row, so buying a package-level cache with
// a test-only escape hatch is a bad trade. Re-reading also means a key added
// after boot takes effect on the next call instead of at the next restart.
func SecretBox() (*secretbox.Box, error) {
	key, err := secretbox.LoadKey(SecretKeyEnv)
	if err != nil {
		// Wrap, don't replace: the underlying error distinguishes "unset" from
		// "not base64" from "wrong length" for the operator reading the log,
		// while errors.Is(err, ErrSealKeyUnset) stays the caller's one check.
		return nil, fmt.Errorf("%w: %v", ErrSealKeyUnset, err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSealKeyUnset, err)
	}
	return box, nil
}

// SealSecret encrypts a source API token for storage in
// import_connection.secret_encrypted. The returned bytes are the only form the
// token may take at rest.
func SealSecret(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, errors.New("imports: refusing to seal an empty secret")
	}
	box, err := SecretBox()
	if err != nil {
		return nil, err
	}
	return box.Seal([]byte(plaintext))
}

// OpenSecret decrypts a stored token. Server-side only: the caller is the
// adapter's HTTP client, and the plaintext must not outlive that request.
func OpenSecret(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", errors.New("imports: connection has no stored secret")
	}
	box, err := SecretBox()
	if err != nil {
		return "", err
	}
	plain, err := box.Open(sealed)
	if err != nil {
		// The ciphertext is authenticated, so a failure here means the row was
		// sealed under a different key (rotation without re-entry) or tampered
		// with. Either way the operator re-enters the token; never fall back to
		// treating the bytes as plaintext.
		return "", fmt.Errorf("imports: stored secret could not be decrypted (wrong key or tampered row): %w", err)
	}
	return string(plain), nil
}
