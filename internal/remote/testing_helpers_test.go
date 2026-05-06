package remote_test

import (
	"context"

	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/memblob"

	"github.com/it-bens/cc-port/internal/remote"
)

// newForTest opens a Remote against a fresh mem:// bucket through
// remote.NewWithMux. The test mux registers memblob and fileblob;
// production registers neither memblob nor an explicit alias for it.
func newForTest(ctx context.Context) (*remote.Remote, error) {
	mux := &blob.URLMux{}
	mux.RegisterBucket(memblob.Scheme, &memblob.URLOpener{})
	mux.RegisterBucket(fileblob.Scheme, &fileblob.URLOpener{})
	return remote.NewWithMux(ctx, "mem://", mux)
}
