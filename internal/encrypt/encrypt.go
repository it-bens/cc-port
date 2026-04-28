// Package encrypt wraps filippo.io/age streams for cc-port archives.
// Symmetric (passphrase) mode only; key-recipient mode is intentionally
// out of scope. The package contributes streaming primitives, pipeline
// stage types whose ReaderStage owns the read-side dispatch matrix
// internally, used by cmd/cc-port and (via the sync plan) cmd/cc-port pull.
package encrypt

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
)

// ErrPassphrase covers wrong-passphrase, tamper, and truncation failures.
// age conflates these by design; callers should not branch on which.
var ErrPassphrase = errors.New("encrypt: bad passphrase or corrupt archive")

// magicPrefix is the age v1 binary-format header. Files always begin
// with this exact byte sequence.
var magicPrefix = []byte("age-encryption.org/v1\n")

// MinPeekLen is the minimum buffer length IsEncrypted needs to make a
// determination. Buffers shorter than this return false.
const MinPeekLen = len("age-encryption.org/v1\n")

// EncryptingWriter returns an io.WriteCloser that encrypts plaintext
// bytes written to it and forwards the ciphertext to dst. The caller
// must Close the returned writer to flush age's authentication trailer.
func EncryptingWriter(dst io.Writer, passphrase string) (io.WriteCloser, error) {
	if passphrase == "" {
		return nil, errors.New("encrypt: empty passphrase")
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("scrypt recipient: %w", err)
	}
	writer, err := age.Encrypt(dst, recipient)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	return writer, nil
}

// DecryptingReader returns an io.Reader that yields plaintext bytes by
// decrypting src. Authentication failures (wrong passphrase, tamper,
// truncation) surface on Read as ErrPassphrase.
func DecryptingReader(src io.Reader, passphrase string) (io.Reader, error) {
	if passphrase == "" {
		return nil, errors.New("encrypt: empty passphrase")
	}
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("scrypt identity: %w", err)
	}
	reader, err := age.Decrypt(src, identity)
	if err != nil {
		return nil, errors.Join(ErrPassphrase, err)
	}
	return &authMappingReader{inner: reader}, nil
}

// authMappingReader maps age's auth errors at Read time onto ErrPassphrase
// so callers do not type-switch on age internals.
type authMappingReader struct{ inner io.Reader }

func (r *authMappingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, errors.Join(ErrPassphrase, err)
	}
	return n, err
}

// IsEncrypted reports whether header begins with the age v1 binary-format
// magic-byte prefix. Buffers shorter than MinPeekLen return false.
func IsEncrypted(header []byte) bool {
	if len(header) < MinPeekLen {
		return false
	}
	return bytes.HasPrefix(header, magicPrefix)
}
