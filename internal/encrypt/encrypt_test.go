package encrypt_test

import (
	"bytes"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/encrypt"
)

// sharedPlaintext is the plaintext behind sharedCipher. The tamper, truncation,
// and header tests only read or mutate ciphertext bytes, so they share one
// scrypt derivation instead of paying one each.
var sharedPlaintext = patternBytes(256)

var sharedCipher = sync.OnceValues(func() ([]byte, error) {
	return encryptTo("passphrase", sharedPlaintext)
})

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 1, 1 << 20} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			plaintext := patternBytes(size)

			cipher := encryptBytes(t, "correct horse battery staple", plaintext)

			reader, err := encrypt.DecryptingReader(bytes.NewReader(cipher), "correct horse battery staple")
			require.NoError(t, err)
			got, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, plaintext, got)
		})
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	t.Parallel()
	cipher := encryptBytes(t, "alpha", []byte("plaintext"))

	_, err := encrypt.DecryptingReader(bytes.NewReader(cipher), "bravo")
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestDecryptTamperedBody(t *testing.T) {
	t.Parallel()
	cipher, err := sharedCipher()
	require.NoError(t, err)
	cipher = bytes.Clone(cipher)
	require.Greater(t, len(cipher), 200, "cipher size sanity")
	cipher[len(cipher)-1] ^= 0xFF

	reader, err := encrypt.DecryptingReader(bytes.NewReader(cipher), "passphrase")
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestDecryptTruncated(t *testing.T) {
	t.Parallel()
	cipher, err := sharedCipher()
	require.NoError(t, err)
	require.Greater(t, len(cipher), 32, "cipher size sanity")
	truncated := cipher[:len(cipher)-16]

	reader, err := encrypt.DecryptingReader(bytes.NewReader(truncated), "passphrase")
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestIsEncrypted(t *testing.T) {
	t.Parallel()
	t.Run("matches age header", func(t *testing.T) {
		cipher, err := sharedCipher()
		require.NoError(t, err)
		require.True(t, encrypt.IsEncrypted(cipher))
	})
	t.Run("rejects plaintext", func(t *testing.T) {
		require.False(t, encrypt.IsEncrypted([]byte("PK\x03\x04..............")))
	})
	t.Run("rejects buffer shorter than MinPeekLen", func(t *testing.T) {
		short := make([]byte, encrypt.MinPeekLen-1)
		copy(short, "age-encryption.org/v")
		require.False(t, encrypt.IsEncrypted(short))
	})
}

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
