package credentials

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePrompter struct {
	answers map[string]string
	calls   []string
	err     error
}

func (f *fakePrompter) Prompt(_ context.Context, label string) (string, error) {
	f.calls = append(f.calls, label)
	if f.err != nil {
		return "", f.err
	}
	return f.answers[label], nil
}

func writeCredsFile(t *testing.T, content string) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "creds.env")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func retrieve(t *testing.T, provider aws.CredentialsProvider) aws.Credentials {
	t.Helper()
	credentials, err := provider.Retrieve(context.Background())
	require.NoError(t, err)
	return credentials
}

func TestResolve_ResolvesFromFileAlone(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")
	path := writeCredsFile(t, "AWS_ACCESS_KEY_ID=AKIAFILE\nAWS_SECRET_ACCESS_KEY=secretFILE\n")

	provider, err := resolveWith(context.Background(), ResolveOptions{Path: path}, nil)

	require.NoError(t, err)
	require.NotNil(t, provider)
	credentials := retrieve(t, provider)
	assert.Equal(t, "AKIAFILE", credentials.AccessKeyID)
	assert.Equal(t, "secretFILE", credentials.SecretAccessKey)
}

func TestResolve_ResolvesFromEnvAlone(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "AKIAENV")
	t.Setenv(envKeySecretAccessKey, "secretENV")
	t.Setenv(envKeySessionToken, "")

	provider, err := resolveWith(context.Background(), ResolveOptions{}, nil)

	require.NoError(t, err)
	require.NotNil(t, provider)
	credentials := retrieve(t, provider)
	assert.Equal(t, "AKIAENV", credentials.AccessKeyID)
	assert.Equal(t, "secretENV", credentials.SecretAccessKey)
}

func TestResolve_FileBeatsEnvOnConflict(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "AKIAENV")
	t.Setenv(envKeySecretAccessKey, "secretENV")
	t.Setenv(envKeySessionToken, "")
	path := writeCredsFile(t, "AWS_ACCESS_KEY_ID=AKIAFILE\nAWS_SECRET_ACCESS_KEY=secretFILE\n")

	provider, err := resolveWith(context.Background(), ResolveOptions{Path: path}, nil)

	require.NoError(t, err)
	credentials := retrieve(t, provider)
	assert.Equal(t, "AKIAFILE", credentials.AccessKeyID)
	assert.Equal(t, "secretFILE", credentials.SecretAccessKey)
}

func TestResolve_FileFillsEnvGap(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "AKIAENV")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")
	path := writeCredsFile(t, "AWS_SECRET_ACCESS_KEY=secretFILE\n")

	provider, err := resolveWith(context.Background(), ResolveOptions{Path: path}, nil)

	require.NoError(t, err)
	credentials := retrieve(t, provider)
	assert.Equal(t, "AKIAENV", credentials.AccessKeyID)
	assert.Equal(t, "secretFILE", credentials.SecretAccessKey)
}

func TestResolve_AllSourcesEmpty_ReturnsNilForSDKFallback(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")

	provider, err := resolveWith(context.Background(), ResolveOptions{}, nil)

	require.NoError(t, err)
	assert.Nil(t, provider)
}

func TestResolve_PromptFillsMissingSecret(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "AKIAENV")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")
	prompter := &fakePrompter{answers: map[string]string{
		envKeySecretAccessKey: "secretPROMPT",
	}}

	provider, err := resolveWith(context.Background(), ResolveOptions{Prompt: true}, prompter)

	require.NoError(t, err)
	credentials := retrieve(t, provider)
	assert.Equal(t, "AKIAENV", credentials.AccessKeyID)
	assert.Equal(t, "secretPROMPT", credentials.SecretAccessKey)
	assert.Equal(t, []string{envKeySecretAccessKey}, prompter.calls,
		"prompt only fires for the missing field")
}

func TestResolve_NoPromptWithMissing_ReturnsIncompleteError(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "AKIAENV")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")

	_, err := resolveWith(context.Background(), ResolveOptions{Prompt: false}, nil)

	var incomplete *IncompleteCredentialsError
	require.ErrorAs(t, err, &incomplete)
	assert.Equal(t, []string{envKeySecretAccessKey}, incomplete.MissingFields)
	assert.Equal(t, []string{"env"}, incomplete.TriedSources)
}

func TestResolve_PromptCanceledByContext_ReturnsCanceled(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "AKIAENV")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")
	prompter := &fakePrompter{err: context.Canceled}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveWith(ctx, ResolveOptions{Prompt: true}, prompter)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestResolve_FileIgnoresUnknownKeys_ResolvesCleanly(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")
	path := writeCredsFile(t, "MY_VAR=foo\nAWS_ACCESS_KEY_ID=AKIA\nAWS_SECRET_ACCESS_KEY=secret\n")

	provider, err := resolveWith(context.Background(), ResolveOptions{Path: path}, nil)

	require.NoError(t, err)
	credentials := retrieve(t, provider)
	assert.Equal(t, "AKIA", credentials.AccessKeyID)
	assert.Equal(t, "secret", credentials.SecretAccessKey)
}

func TestResolve_FileEmptyHardError(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")
	path := writeCredsFile(t, "# only comments\n")

	_, err := resolveWith(context.Background(), ResolveOptions{Path: path}, nil)

	var parseErr *FileParseError
	require.ErrorAs(t, err, &parseErr)
	assert.Equal(t, 0, parseErr.Line)
}

func TestResolve_PromptUnavailableSurfaced(t *testing.T) {
	t.Setenv(envKeyAccessKeyID, "AKIAENV")
	t.Setenv(envKeySecretAccessKey, "")
	t.Setenv(envKeySessionToken, "")
	prompter := &fakePrompter{err: ErrPromptUnavailable}

	_, err := resolveWith(context.Background(), ResolveOptions{Prompt: true}, prompter)

	assert.ErrorIs(t, err, ErrPromptUnavailable)
}
