package remote_test

import (
	"context"

	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/memblob"

	"github.com/it-bens/cc-port/internal/remote"
)

// newForTest opens a Remote through remote.NewWithMux against a test
// mux that registers memblob and fileblob. Production never registers
// memblob; this helper scopes that registration to test scope only.
//
//nolint:unparam // rawURL is intentionally general; current callers all pass memURL but file:// tests may follow.
func newForTest(ctx context.Context, rawURL string) (*remote.Remote, error) {
	mux := &blob.URLMux{}
	mux.RegisterBucket(memblob.Scheme, &memblob.URLOpener{})
	mux.RegisterBucket(fileblob.Scheme, &fileblob.URLOpener{})
	return remote.NewWithMux(ctx, rawURL, mux)
}
