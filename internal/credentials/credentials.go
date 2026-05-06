// Package credentials resolves AWS credentials for cc-port's s3 remote
// from a layered file > env > prompt source list.
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

// Resolve walks file > env > prompt and returns a static-credentials
// provider, (nil, nil) when no source contributed (SDK chain fallback),
// or an error when a contribution was incomplete. ctx cancellation
// during the prompt returns context.Canceled wrapped via fmt.Errorf.
func Resolve(ctx context.Context, opts ResolveOptions) (aws.CredentialsProvider, error) {
	return resolveWith(ctx, opts, osTTYPrompter{})
}
