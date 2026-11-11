// Package keyring encrypts signing secrets at rest with AES-256-GCM.
//
// A keyring is configured as a comma-separated list of keys, each written as
// a key ID and the base64-encoded (standard encoding, padded) 32-byte key:
//
//	<id>:<base64 key>[,<id>:<base64 key>...]
//
// The first key is the current key and encrypts everything new. The others
// are older keys, kept so that ciphertexts they made can still be decrypted.
// To rotate, put a new key first and keep the old ones after it. Key IDs are
// 1 to 255 characters from [A-Za-z0-9_-], for example 2026-11. A key can be
// generated with `openssl rand -base64 32`.
//
// A ciphertext is laid out as
//
//	len(id) (1 byte) | id | nonce (12 bytes) | sealed plaintext | tag (16 bytes)
//
// with a random nonce for each encryption. The header (length and key ID) is
// authenticated together with the caller's associated data, such as the ID of
// the Endpoint the signing secret belongs to, so a ciphertext copied onto
// another Endpoint's row fails to decrypt.
package keyring

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const keySize = 32 // AES-256

// ErrUnknownKey means a ciphertext was encrypted with a key that is not in the
// keyring, typically because the key was removed while still in use.
var ErrUnknownKey = errors.New("unknown encryption key")

// Keyring holds the current key, which encrypts, and older keys, which only
// decrypt. It is safe for concurrent use.
type Keyring struct {
	currentID string
	keys      map[string]cipher.AEAD
}

// Parse reads a keyring from its config form, described in the package
// documentation. Error messages name key IDs but never include key material.
func Parse(s string) (*Keyring, error) {
	k := &Keyring{keys: make(map[string]cipher.AEAD)}
	for i, entry := range strings.Split(s, ",") {
		id, encoded, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("parse keyring entry %d: want <id>:<base64 key>", i)
		}
		if !validKeyID(id) {
			return nil, fmt.Errorf("parse keyring entry %d: invalid key ID %q", i, id)
		}
		if _, dup := k.keys[id]; dup {
			return nil, fmt.Errorf("parse keyring entry %d: duplicate key ID %q", i, id)
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("parse key %q: %w", id, err)
		}
		if len(key) != keySize {
			return nil, fmt.Errorf("parse key %q: got %d bytes, want %d", id, len(key), keySize)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("parse key %q: %w", id, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("parse key %q: %w", id, err)
		}
		if i == 0 {
			k.currentID = id
		}
		k.keys[id] = aead
	}
	return k, nil
}

// validKeyID reports whether id fits in the one-byte length prefix and uses
// only [A-Za-z0-9_-].
func validKeyID(id string) bool {
	if len(id) == 0 || len(id) > 255 {
		return false
	}
	for _, c := range []byte(id) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// Encrypt encrypts plaintext with the current key, bound to ad.
func (k *Keyring) Encrypt(plaintext, ad []byte) ([]byte, error) {
	aead := k.keys[k.currentID]
	header := append([]byte{byte(len(k.currentID))}, k.currentID...)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	out := append(header, nonce...)
	return aead.Seal(out, nonce, plaintext, associatedData(header, ad)), nil
}

// Decrypt decrypts a ciphertext made by Encrypt with the same ad.
func (k *Keyring) Decrypt(ciphertext, ad []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(ciphertext) < 1+int(ciphertext[0]) {
		return nil, errors.New("decrypt: ciphertext too short")
	}
	header := ciphertext[:1+int(ciphertext[0])]
	aead, ok := k.keys[string(header[1:])]
	if !ok {
		return nil, fmt.Errorf("decrypt: key %q: %w", header[1:], ErrUnknownKey)
	}
	rest := ciphertext[len(header):]
	if len(rest) < aead.NonceSize() {
		return nil, errors.New("decrypt: ciphertext too short")
	}
	nonce, sealed := rest[:aead.NonceSize()], rest[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, sealed, associatedData(header, ad))
	if err != nil {
		return nil, errors.New("decrypt: message authentication failed")
	}
	return plaintext, nil
}

// associatedData authenticates the ciphertext header along with the caller's
// ad. It copies into a new slice: appending to header could overwrite the
// bytes that follow it in the ciphertext.
func associatedData(header, ad []byte) []byte {
	return append(append(make([]byte, 0, len(header)+len(ad)), header...), ad...)
}
