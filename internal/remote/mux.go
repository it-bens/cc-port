package remote

import (
	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/s3blob"
)

// buildMux constructs the cc-port-owned URL multiplexer. The full set
// of supported schemes is registered here in one place; adding a
// backend means writing a BucketURLOpener (one new file) and adding
// one RegisterBucket line below.
func buildMux(deps Deps) *blob.URLMux {
	mux := &blob.URLMux{}
	mux.RegisterBucket(fileblob.Scheme, &fileblob.URLOpener{})
	mux.RegisterBucket(s3blob.Scheme, newS3Opener(deps))
	return mux
}
