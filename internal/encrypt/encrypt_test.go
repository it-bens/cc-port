package encrypt_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/encrypt"
)

func TestRoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, 1 << 20} {
		t.Run("", func(t *testing.T) {
			plaintext := make([]byte, size)
			_, err := rand.Read(plaintext)
			require.NoError(t, err)

			var ciphertext bytes.Buffer
			writer, err := encrypt.EncryptingWriter(&ciphertext, "correct horse battery staple")
			require.NoError(t, err)
			_, err = writer.Write(plaintext)
			require.NoError(t, err)
			require.NoError(t, writer.Close())

			reader, err := encrypt.DecryptingReader(&ciphertext, "correct horse battery staple")
			require.NoError(t, err)
			got, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, plaintext, got)
		})
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	var ciphertext bytes.Buffer
	writer, err := encrypt.EncryptingWriter(&ciphertext, "alpha")
	require.NoError(t, err)
	_, err = writer.Write([]byte("plaintext"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	_, err = encrypt.DecryptingReader(&ciphertext, "bravo")
	require.Error(t, err)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestDecryptTamperedBody(t *testing.T) {
	var ciphertext bytes.Buffer
	writer, err := encrypt.EncryptingWriter(&ciphertext, "passphrase")
	require.NoError(t, err)
	_, err = writer.Write([]byte("plaintext content for tampering"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	bytesCipher := ciphertext.Bytes()
	require.Greater(t, len(bytesCipher), 200, "cipher size sanity")
	bytesCipher[len(bytesCipher)-1] ^= 0xFF

	reader, err := encrypt.DecryptingReader(bytes.NewReader(bytesCipher), "passphrase")
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestDecryptTruncated(t *testing.T) {
	var ciphertext bytes.Buffer
	writer, err := encrypt.EncryptingWriter(&ciphertext, "passphrase")
	require.NoError(t, err)
	_, err = writer.Write([]byte("plaintext content for truncation"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	bytesCipher := ciphertext.Bytes()
	require.Greater(t, len(bytesCipher), 32, "cipher size sanity")
	truncated := bytesCipher[:len(bytesCipher)-16]

	reader, err := encrypt.DecryptingReader(bytes.NewReader(truncated), "passphrase")
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestIsEncrypted(t *testing.T) {
	t.Run("matches age header", func(t *testing.T) {
		var ciphertext bytes.Buffer
		writer, err := encrypt.EncryptingWriter(&ciphertext, "passphrase")
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		require.True(t, encrypt.IsEncrypted(ciphertext.Bytes()))
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
