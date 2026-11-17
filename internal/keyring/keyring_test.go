package keyring_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/keyring"
)

// Test keys: 32 bytes of 0x01 and 0x02.
//
// A ciphertext made with key ID "k1" is laid out as: [0] ID length, [1:3] ID,
// [3:15] nonce, then the sealed secret and a 16-byte tag.
const (
	key1 = "k1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	key2 = "k2:AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, key1)
	ad := []byte("ep_1")
	ciphertext := mustEncrypt(t, kr, "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", ad)

	got, err := kr.Decrypt(ciphertext, ad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if diff := cmp.Diff("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", string(got)); diff != "" {
		t.Errorf("Decrypt (-want +got):\n%s", diff)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, key1)
	ad := []byte("ep_1")
	first := mustEncrypt(t, kr, "whsec_secret", ad)
	second := mustEncrypt(t, kr, "whsec_secret", ad)
	if bytes.Equal(first, second) {
		t.Errorf("Encrypt returned the same ciphertext twice: %x", first)
	}
}

func TestEncryptUsesFirstKeyAsCurrent(t *testing.T) {
	t.Parallel()

	ad := []byte("ep_1")
	ciphertext := mustEncrypt(t, mustParse(t, key2+","+key1), "whsec_new", ad)

	if _, err := mustParse(t, key2).Decrypt(ciphertext, ad); err != nil {
		t.Errorf("Decrypt with k2: %v", err)
	}
	if _, err := mustParse(t, key1).Decrypt(ciphertext, ad); !errors.Is(err, keyring.ErrUnknownKey) {
		t.Errorf("Decrypt with k1 error = %v, want ErrUnknownKey", err)
	}
}

func TestDecryptWithOlderKeyAfterRotation(t *testing.T) {
	t.Parallel()

	ad := []byte("ep_1")
	ciphertext := mustEncrypt(t, mustParse(t, key1), "whsec_old", ad)

	got, err := mustParse(t, key2+","+key1).Decrypt(ciphertext, ad)
	if err != nil {
		t.Fatalf("Decrypt after rotation: %v", err)
	}
	if diff := cmp.Diff("whsec_old", string(got)); diff != "" {
		t.Errorf("Decrypt (-want +got):\n%s", diff)
	}
}

// TestDecryptRejectsTamperedCiphertext covers the bytes GCM itself
// authenticates. Changes to the header are covered by
// TestDecryptRejectsRewrittenKeyID and TestDecryptUnknownKey.
func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, key1)
	ad := []byte("ep_1")
	ciphertext := mustEncrypt(t, kr, "whsec_secret", ad)

	tests := []struct {
		name  string
		index int
	}{
		{"nonce", 3},
		{"sealed secret", 15},
		{"tag", len(ciphertext) - 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tampered := slices.Clone(ciphertext)
			tampered[tt.index] ^= 0x01
			if got, err := kr.Decrypt(tampered, ad); err == nil {
				t.Errorf("Decrypt succeeded with %q, want error", got)
			}
		})
	}
}

// TestDecryptRejectsRewrittenKeyID checks that the key ID is authenticated.
// Both IDs name the same key, so the rewritten ciphertext would decrypt if
// only the key it selects were checked.
func TestDecryptRejectsRewrittenKeyID(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "a:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=,b:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	ad := []byte("ep_1")
	ciphertext := mustEncrypt(t, kr, "whsec_secret", ad)

	ciphertext[1] = 'b' // [0] ID length, [1] ID
	if got, err := kr.Decrypt(ciphertext, ad); err == nil {
		t.Errorf("Decrypt succeeded with %q, want error", got)
	}
}

func TestDecryptRejectsOtherEndpointsAssociatedData(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, key1)
	ciphertext := mustEncrypt(t, kr, "whsec_secret", []byte("ep_1"))

	if got, err := kr.Decrypt(ciphertext, []byte("ep_2")); err == nil {
		t.Errorf("Decrypt succeeded with %q, want error", got)
	}
}

func TestDecryptUnknownKey(t *testing.T) {
	t.Parallel()

	ad := []byte("ep_1")
	ciphertext := mustEncrypt(t, mustParse(t, key1), "whsec_secret", ad)

	_, err := mustParse(t, key2).Decrypt(ciphertext, ad)
	if !errors.Is(err, keyring.ErrUnknownKey) {
		t.Errorf("Decrypt error = %v, want ErrUnknownKey", err)
	}
}

func TestDecryptRejectsTruncatedCiphertext(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, key1)
	ad := []byte("ep_1")
	ciphertext := mustEncrypt(t, kr, "whsec_secret", ad)

	tests := []struct {
		name string
		len  int
	}{
		{"empty", 0},
		{"inside key ID", 2},
		{"inside nonce", 10},
		{"inside tag", len(ciphertext) - 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, err := kr.Decrypt(ciphertext[:tt.len], ad); err == nil {
				t.Errorf("Decrypt succeeded with %q, want error", got)
			}
		})
	}
}

func TestParseRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{"empty", "", "keyring entry 0: want <id>:<base64 key>"},
		{"missing separator", "k1AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=", "keyring entry 0: want <id>:<base64 key>"},
		{"trailing comma", key1 + ",", "keyring entry 1: want <id>:<base64 key>"},
		{"empty key ID", ":AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=", `keyring entry 0: invalid key ID ""`},
		{"key ID with space", "k 1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=", `keyring entry 0: invalid key ID "k 1"`},
		{"space after comma", key1 + ", " + key2, `keyring entry 1: invalid key ID " k2"`},
		{"key ID over 255 bytes", strings.Repeat("k", 256) + ":AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=", "keyring entry 0: invalid key ID"},
		{"bad base64", "k1:not base64!", `keyring entry 0: key "k1": illegal base64 data`},
		{"16-byte key", "k1:AQEBAQEBAQEBAQEBAQEBAQ==", `keyring entry 0: key "k1" has 16 bytes, want 32`},
		{"duplicate key ID", key1 + ",k1:AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=", `keyring entry 1: duplicate key ID "k1"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := keyring.Parse(tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse(%q) error = %v, want one containing %q", tt.config, err, tt.wantErr)
			}
		})
	}
}

// storedCiphertext was made by Encrypt with key1 and associated data "ep_1".
// Ciphertexts are stored, so it must keep decrypting whatever the code
// becomes. It is also the "valid" fuzz seed in testdata/fuzz/FuzzDecrypt.
const storedCiphertext = "\x02k1\xe1\xb6\xce\x37\x43\x2c\x64\xeb\x80\x87\x08\xaf\x50\xbb\xc7\xed\xbe\x29\x87\x14\xcf\x9b\x1d\x96\x93\x12\x81\xca\xc0\xa1\x1f\xc3\x5a\x9f\x08\xb5\xda\x5f\x21\x1b"

func TestDecryptStoredCiphertext(t *testing.T) {
	t.Parallel()

	got, err := mustParse(t, key1).Decrypt([]byte(storedCiphertext), []byte("ep_1"))
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if diff := cmp.Diff("whsec_secret", string(got)); diff != "" {
		t.Errorf("Decrypt (-want +got):\n%s", diff)
	}
}

// FuzzDecrypt checks that Decrypt never panics, and that any input it accepts
// decrypts to the only plaintext ever encrypted under these keys. Seeds are in
// testdata/fuzz/FuzzDecrypt.
func FuzzDecrypt(f *testing.F) {
	kr, err := keyring.Parse(key2 + "," + key1)
	if err != nil {
		f.Fatalf("Parse: %v", err)
	}

	f.Fuzz(func(t *testing.T, ciphertext []byte) {
		got, err := kr.Decrypt(ciphertext, []byte("ep_1"))
		if err == nil && string(got) != "whsec_secret" {
			t.Errorf("Decrypt(%x) succeeded with %q, want error", ciphertext, got)
		}
	})
}

func mustParse(t *testing.T, config string) *keyring.Keyring {
	t.Helper()
	kr, err := keyring.Parse(config)
	if err != nil {
		t.Fatalf("Parse(%q): %v", config, err)
	}
	return kr
}

func mustEncrypt(t *testing.T, kr *keyring.Keyring, plaintext string, associatedData []byte) []byte {
	t.Helper()
	ciphertext, err := kr.Encrypt([]byte(plaintext), associatedData)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return ciphertext
}
