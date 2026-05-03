// Package remote wraps gocloud.dev/blob with a narrow consumer-defined
// surface. The Remote type exposes Open, Create, Stat, and Close. URL
// dispatch is delegated to gocloud.dev (file:// for local filesystem,
// s3:// for AWS S3 and S3-compatible stores). Backend-native
// authentication (e.g. the AWS SDK credential chain) is the operator's
// environment responsibility, not cc-port state.
package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"gocloud.dev/blob"
	// Blank-imported so blob.OpenBucket dispatches file:// URLs to the
	// filesystem driver. Adding a backend means blank-importing it here.
	_ "gocloud.dev/blob/fileblob"
	// Blank-imported so blob.OpenBucket dispatches s3:// URLs to the
	// AWS S3 driver. Authentication uses the AWS SDK credential chain.
	_ "gocloud.dev/blob/s3blob"
	"gocloud.dev/gcerrors"
)

// New opens a Remote for the given URL. Supported schemes:
//   - file:///path/to/dir
//   - s3://bucket/optional-prefix?region=<region>
func New(ctx context.Context, rawURL string) (*Remote, error) {
	bucket, err := blob.OpenBucket(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("remote: open bucket %q: %w", rawURL, err)
	}
	return &Remote{bucket: bucket, url: rawURL}, nil
}

// Remote is a handle to a configured blob backend. Construct via New
// and release via Close.
type Remote struct {
	bucket *blob.Bucket
	url    string
}

// URL returns the URL the Remote was opened with. Useful for logging.
func (r *Remote) URL() string { return r.url }

// Open returns a Reader for the archive at name. The Reader carries
// the content length reported by the bucket without a stat round trip.
// Returns ErrNotFound when the key is absent.
func (r *Remote) Open(ctx context.Context, name string) (*Reader, error) {
	rc, err := r.bucket.NewReader(ctx, name, nil)
	if err != nil {
		if gcerrors.Code(err) == gcerrors.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("remote: open %q: %w", name, err)
	}
	return &Reader{inner: rc}, nil
}

// Create returns a writer for the archive at name. Failure to close
// the returned writer means the archive is not visible on the remote.
func (r *Remote) Create(ctx context.Context, name string) (io.WriteCloser, error) {
	w, err := r.bucket.NewWriter(ctx, name, nil)
	if err != nil {
		return nil, fmt.Errorf("remote: create %q: %w", name, err)
	}
	return w, nil
}

// Stat returns size and modification time. Returns ErrNotFound when
// the key is absent.
func (r *Remote) Stat(ctx context.Context, name string) (Attributes, error) {
	attrs, err := r.bucket.Attributes(ctx, name)
	if err != nil {
		if gcerrors.Code(err) == gcerrors.NotFound {
			return Attributes{}, ErrNotFound
		}
		return Attributes{}, fmt.Errorf("remote: stat %q: %w", name, err)
	}
	return Attributes{Size: attrs.Size, ModTime: attrs.ModTime}, nil
}

// Close releases the bucket connection. Idempotent.
func (r *Remote) Close() error { return r.bucket.Close() }

// Attributes is the subset of blob.Attributes that cc-port consumes.
type Attributes struct {
	Size    int64
	ModTime time.Time
}

// Reader is the handle returned by Remote.Open. It carries the content
// length the bucket reported on open so callers do not stat separately.
// Wraps *blob.Reader without leaking the gocloud type into the surface.
type Reader struct {
	inner *blob.Reader
}

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) { return r.inner.Read(p) }

// Close implements io.Closer.
func (r *Reader) Close() error { return r.inner.Close() }

// Size returns the content length reported by the bucket on open.
func (r *Reader) Size() int64 { return r.inner.Size() }

// ErrNotFound is returned by Open and Stat when the requested key is
// absent on the remote. Backend-specific not-found errors are
// translated to this sentinel.
var ErrNotFound = errors.New("remote: archive not found")
