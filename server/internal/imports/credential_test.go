package imports

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

const fakeLinearToken = "lin_api_00TESTTOKENDONOTSHIP00"

func testSealKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// A missing seal key has exactly one outcome: refuse. This is the unit-level
// half of the endpoint's 503 — there is no branch that stores the token in the
// clear, so there is no way for one to be added without this test failing.
func TestSealSecretFailsClosedWithoutKey(t *testing.T) {
	t.Setenv(SecretKeyEnv, "")

	if _, err := SecretBox(); !errors.Is(err, ErrSealKeyUnset) {
		t.Fatalf("SecretBox() err = %v, want ErrSealKeyUnset", err)
	}
	sealed, err := SealSecret(fakeLinearToken)
	if !errors.Is(err, ErrSealKeyUnset) {
		t.Fatalf("SealSecret err = %v, want ErrSealKeyUnset", err)
	}
	if sealed != nil {
		t.Fatalf("SealSecret returned %d bytes with no key; must return nothing to store", len(sealed))
	}
	if _, err := OpenSecret([]byte("whatever")); !errors.Is(err, ErrSealKeyUnset) {
		t.Fatalf("OpenSecret err = %v, want ErrSealKeyUnset", err)
	}
}

// A key that is present but unusable (not base64, wrong length) is the same
// refusal, not a panic and not a zero key.
func TestSealSecretFailsClosedOnMalformedKey(t *testing.T) {
	for _, tc := range []struct{ name, key string }{
		{"not base64", "!!!not-base64!!!"},
		{"too short", base64.StdEncoding.EncodeToString([]byte("16-bytes-exactly"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(SecretKeyEnv, tc.key)
			if _, err := SealSecret(fakeLinearToken); !errors.Is(err, ErrSealKeyUnset) {
				t.Fatalf("SealSecret err = %v, want ErrSealKeyUnset", err)
			}
		})
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	t.Setenv(SecretKeyEnv, testSealKey(t))

	sealed, err := SealSecret(fakeLinearToken)
	if err != nil {
		t.Fatalf("SealSecret: %v", err)
	}
	if strings.Contains(string(sealed), fakeLinearToken) {
		t.Fatal("sealed bytes contain the plaintext token")
	}
	got, err := OpenSecret(sealed)
	if err != nil {
		t.Fatalf("OpenSecret: %v", err)
	}
	if got != fakeLinearToken {
		t.Fatalf("round trip = %q, want %q", got, fakeLinearToken)
	}

	// Sealing twice must not be deterministic — a token must not be
	// fingerprintable by its ciphertext.
	again, err := SealSecret(fakeLinearToken)
	if err != nil {
		t.Fatalf("SealSecret (second): %v", err)
	}
	if string(again) == string(sealed) {
		t.Fatal("two seals of the same token produced identical ciphertext")
	}
}

func TestOpenSecretRejectsWrongKeyAndTamper(t *testing.T) {
	t.Setenv(SecretKeyEnv, testSealKey(t))
	sealed, err := SealSecret(fakeLinearToken)
	if err != nil {
		t.Fatalf("SealSecret: %v", err)
	}

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xFF
	if _, err := OpenSecret(tampered); err == nil {
		t.Fatal("OpenSecret accepted a tampered ciphertext")
	}

	t.Setenv(SecretKeyEnv, testSealKey(t))
	if _, err := OpenSecret(sealed); err == nil {
		t.Fatal("OpenSecret decrypted a row sealed under a different key")
	}
}

func TestSealSecretRefusesEmpty(t *testing.T) {
	t.Setenv(SecretKeyEnv, testSealKey(t))
	if _, err := SealSecret(""); err == nil {
		t.Fatal("SealSecret(\"\") succeeded; an empty secret must not be storable")
	}
}

// The structural half of "the token is never returned by any endpoint": the
// row types the listing/detail queries scan into have no secret field at all,
// so no response assembled from them — or from a lazy json.Marshal of the row
// itself — can carry one. Only GetImportConnectionSecret returns the sealed
// column, and it is named for what it is.
func TestConnectionRowTypesCarryNoSecret(t *testing.T) {
	const marker = "SEALED-TOKEN-BYTES"

	rows := []any{
		db.ListImportConnectionsRow{Source: "linear", Label: "acme"},
		db.GetImportConnectionRow{Source: "linear", Label: "acme"},
		db.CreateImportConnectionRow{Source: "linear", Label: "acme"},
		db.UpdateImportConnectionProbeRow{Source: "linear", ProbeStatus: "ok"},
	}
	for _, row := range rows {
		blob, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("marshal %T: %v", row, err)
		}
		if strings.Contains(string(blob), "secret") {
			t.Errorf("%T marshals a secret-bearing field: %s", row, blob)
		}
	}

	// The one type that does hold the sealed bytes is the full model, which is
	// reachable only through GetImportConnectionSecret. Assert the sealed
	// column is all it ever exposes — i.e. that nothing put a plaintext
	// `token` field on the model.
	full := db.ImportConnection{Source: "linear", SecretEncrypted: []byte(marker)}
	blob, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal ImportConnection: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatalf("unmarshal ImportConnection: %v", err)
	}
	for name := range fields {
		if strings.Contains(name, "secret") && name != "secret_encrypted" {
			t.Errorf("ImportConnection has an unexpected secret field %q", name)
		}
	}
	if _, ok := fields["secret_encrypted"]; !ok {
		t.Error("ImportConnection lost secret_encrypted; the adapter has nothing to decrypt")
	}
}
