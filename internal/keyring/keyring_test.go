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
		name   string
		config string
	}{
		{"empty", ""},
		{"missing separator", "k1AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="},
		{"trailing comma", key1 + ","},
		{"empty key ID", ":AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="},
		{"key ID with space", "k 1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="},
		{"space after comma", key1 + ", " + key2},
		{"key ID over 255 bytes", strings.Repeat("k", 256) + ":AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="},
		{"bad base64", "k1:not base64!"},
		{"16-byte key", "k1:AQEBAQEBAQEBAQEBAQEBAQ=="},
		{"duplicate key ID", key1 + ",k1:AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := keyring.Parse(tt.config); err == nil {
				t.Errorf("Parse(%q) succeeded, want error", tt.config)
			}
		})
	}
}

// FuzzDecrypt checks that Decrypt never panics, and that any input it accepts
// decrypts to the only plaintext ever encrypted under these keys. Each fuzz
// worker runs this setup again with a fresh random nonce, so a ciphertext from
// another worker is genuine too; comparing ciphertexts would flag it.
func FuzzDecrypt(f *testing.F) {
	kr, err := keyring.Parse(key2 + "," + key1)
	if err != nil {
		f.Fatalf("Parse: %v", err)
	}
	ad := []byte("ep_1")
	valid, err := kr.Encrypt([]byte("whsec_secret"), ad)
	if err != nil {
		f.Fatalf("Encrypt: %v", err)
	}
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Add([]byte("\x02k1"))
	f.Add([]byte("\x02k1\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))

	f.Fuzz(func(t *testing.T, ciphertext []byte) {
		got, err := kr.Decrypt(ciphertext, ad)
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
