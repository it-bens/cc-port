// Package credentials resolves AWS credentials for cc-port's s3 remote
// from a layered set of sources: a .env-style credentials file, AWS_*
// environment variables, and an interactive TTY prompt. The resolved
// set is handed to the AWS SDK as a static-credentials provider; if no
// source contributes any field, Resolve returns nil and the caller
// falls back to the SDK default credential chain.
package credentials

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// ResolveOptions configures Resolve.
type ResolveOptions struct {
	// Path is the path to a .env-style credentials file. The empty
	// string disables the file source. When Path is set, the file's
	// fields take precedence over env on conflicts.
	Path string

	// Prompt enables an interactive TTY prompt for required fields
	// still missing after file and env sources have run. False forces
	// hard error when fields are missing.
	Prompt bool
}

// Resolve walks the configured sources in precedence order: file (if
// Path set), then env, then prompt (if Prompt is true and a TTY is
// available). Returns a static-credentials provider when at least one
// source contributed a field and the merged set is complete. Returns
// (nil, nil) when no source contributed any field, signaling the
// caller to fall back to the SDK default credential chain. Returns a
// non-nil error when at least one source contributed but the merged
// set still misses a required field and no further source can fill
// it. ctx threads through file IO and the prompt; ctx cancellation
// during the prompt aborts within one read cycle and the function
// returns context.Canceled wrapped via fmt.Errorf("canceled: %w",
// ctx.Err()).
func Resolve(ctx context.Context, opts ResolveOptions) (aws.CredentialsProvider, error) {
	return resolveWith(ctx, opts, osTTYPrompter{})
}
