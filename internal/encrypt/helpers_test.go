package encrypt_test

import (
	"bytes"
	"sync"

	"github.com/it-bens/cc-port/internal/encrypt"
)

// encryptTo returns body's age ciphertext under passphrase without a
// *testing.T, so package-scope caches can share one derivation.
func encryptTo(passphrase string, body []byte) ([]byte, error) {
	var cipher bytes.Buffer
	w, err := encrypt.EncryptingWriter(&cipher, passphrase)
	if err != nil {
		return nil, err
	}
	// age's stream writer accepts an empty body as a no-op.
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return cipher.Bytes(), nil
}

const dispatchPass = "dispatch-passphrase"

// sharedPlaintext is the plaintext behind sharedCipher.
var sharedPlaintext = patternBytes(256)

// sharedCipher is the one dispatchPass ciphertext every test that only reads
// encrypted input shares, saving the encryption-side scrypt derivation instead
// of paying it once per test. Every DecryptingReader call on it still pays its
// own derivation. Tests share the cached bytes read-only; a test that mutates
// them works on a bytes.Clone copy.
var sharedCipher = sync.OnceValues(func() ([]byte, error) {
	return encryptTo(dispatchPass, sharedPlaintext)
})

// patternBytes returns size bytes filled with a deterministic byte pattern.
// Avoids crypto/rand so the round-trip test stays reproducible while
// covering all 256 byte values.
func patternBytes(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}
