package remote

import (
	"context"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"gocloud.dev/blob"
	"gocloud.dev/blob/s3blob"
)

// s3Opener is cc-port's BucketURLOpener for s3:// URLs. It owns the
// URL-param-stripping dance that gocloud.dev's s3blob.URLOpener does
// internally, plus an optional credentials-provider override that
// makes interactive / file-based credential resolution possible.
//
// When credentials is nil, the opener leaves the SDK config's
// Credentials at the gocloud default, preserving the AWS SDK chain
// (~/.aws/credentials with ?profile=, IAM role, IMDS).
type s3Opener struct {
	credentials aws.CredentialsProvider
}

func newS3Opener(deps Deps) *s3Opener {
	return &s3Opener{credentials: deps.Credentials}
}

func (o *s3Opener) OpenBucketURL(ctx context.Context, u *url.URL) (*blob.Bucket, error) {
	stock := &s3blob.URLOpener{}
	return stock.OpenBucketURL(ctx, u)
}
