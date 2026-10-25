package encrypt_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/encrypt"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"1 MiB multi-chunk", 1 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plaintext := patternBytes(tc.size)

			cipher, err := encryptTo("correct horse battery staple", plaintext)
			require.NoError(t, err)

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
	cipher, err := sharedCipher()
	require.NoError(t, err)

	_, err = encrypt.DecryptingReader(bytes.NewReader(cipher), "wrong-passphrase")
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestDecryptTamperedBody(t *testing.T) {
	t.Parallel()
	cipher, err := sharedCipher()
	require.NoError(t, err)
	cipher = bytes.Clone(cipher)
	require.Greater(t, len(cipher), 200, "cipher size sanity")
	cipher[len(cipher)-1] ^= 0xFF

	reader, err := encrypt.DecryptingReader(bytes.NewReader(cipher), dispatchPass)
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestIsEncrypted(t *testing.T) {
	t.Parallel()
	t.Run("rejects plaintext", func(t *testing.T) {
		assert.False(t, encrypt.IsEncrypted([]byte("PK\x03\x04..............")))
	})
	t.Run("rejects a truncated magic prefix", func(t *testing.T) {
		assert.False(t, encrypt.IsEncrypted([]byte("age-encryption.org/v1")))
	})
}
