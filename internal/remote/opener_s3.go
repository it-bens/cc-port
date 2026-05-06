package remote

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	gcaws "gocloud.dev/aws"
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

// s3BlobOptionsFromURL captures the s3blob- and s3-Options-level knobs
// extracted from the URL query. encryptionType / kmsKeyID feed into
// s3blob.Options; accelerate / usePathStyle / disableHTTPS feed into
// the *s3.Options modifier function passed to s3.NewFromConfig.
type s3BlobOptionsFromURL struct {
	encryptionType string
	kmsKeyID       string
	accelerate     bool
	usePathStyle   bool
	disableHTTPS   bool
}

const queryTrue = "true"

// stripS3BlobParams removes every s3blob-specific or s3.Options-level
// query parameter from values and returns the residue together with
// the extracted options. The residue is fed to V2ConfigFromURLParams,
// which errors on unknown query parameters; stripping known ones is
// gocloud's documented contract for callers using OpenBucket directly.
func stripS3BlobParams(values url.Values) (url.Values, s3BlobOptionsFromURL) {
	options := s3BlobOptionsFromURL{
		encryptionType: values.Get("ssetype"),
		kmsKeyID:       values.Get("kmskeyid"),
		accelerate:     values.Get("accelerate") == queryTrue,
		disableHTTPS:   values.Get("disable_https") == queryTrue,
	}
	if values.Get("use_path_style") == queryTrue || values.Get("s3ForcePathStyle") == queryTrue {
		options.usePathStyle = true
	}
	stripped := url.Values{}
	for key, list := range values {
		switch key {
		case "ssetype", "kmskeyid", "accelerate", "use_path_style", "s3ForcePathStyle", "disable_https":
			continue
		}
		stripped[key] = append([]string(nil), list...)
	}
	return stripped, options
}

// awsConfigFromURL parses query parameters via gocloud's
// V2ConfigFromURLParams and applies the credentials override from
// o.credentials when non-nil.
func (o *s3Opener) awsConfigFromURL(ctx context.Context, stripped url.Values) (aws.Config, error) {
	config, err := gcaws.V2ConfigFromURLParams(ctx, stripped)
	if err != nil {
		return aws.Config{}, fmt.Errorf("remote: parse s3 url params: %w", err)
	}
	if o.credentials != nil {
		config.Credentials = o.credentials
	}
	return config, nil
}

func (o *s3Opener) OpenBucketURL(ctx context.Context, u *url.URL) (*blob.Bucket, error) {
	stripped, options := stripS3BlobParams(u.Query())

	config, err := o.awsConfigFromURL(ctx, stripped)
	if err != nil {
		return nil, err
	}

	client := awss3.NewFromConfig(config, func(opts *awss3.Options) {
		if options.usePathStyle {
			opts.UsePathStyle = true
		}
		if options.accelerate {
			opts.UseAccelerate = true
		}
		if options.disableHTTPS {
			opts.EndpointOptions = awss3.EndpointResolverOptions{DisableHTTPS: true}
		}
	})

	bucket, err := s3blob.OpenBucket(ctx, client, u.Host, &s3blob.Options{
		EncryptionType:  awstypes.ServerSideEncryption(options.encryptionType),
		KMSEncryptionID: options.kmsKeyID,
	})
	if err != nil {
		return nil, fmt.Errorf("remote: open s3 bucket %q: %w", u.Host, err)
	}

	prefix := strings.TrimPrefix(u.Path, "/")
	if prefix != "" {
		bucket = blob.PrefixedBucket(bucket, prefix+"/")
	}
	return bucket, nil
}
