package remote

import (
	"context"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gcaws "gocloud.dev/aws"
)

func TestStripS3BlobParams_ExtractsKnownKeys(t *testing.T) {
	rawURL := "s3://bucket/team-a?region=us-east-1&endpoint=foo&ssetype=aws:kms&kmskeyid=arn&accelerate=true&use_path_style=true&disable_https=true"
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)

	stripped, options := stripS3BlobParams(parsed.Query())

	assert.Equal(t, "aws:kms", options.encryptionType)
	assert.Equal(t, "arn", options.kmsKeyID)
	assert.True(t, options.accelerate)
	assert.True(t, options.usePathStyle)
	assert.True(t, options.disableHTTPS)

	assert.Equal(t, "us-east-1", stripped.Get("region"))
	assert.Equal(t, "foo", stripped.Get("endpoint"))
	assert.Empty(t, stripped.Get("ssetype"))
	assert.Empty(t, stripped.Get("kmskeyid"))
	assert.Empty(t, stripped.Get("accelerate"))
	assert.Empty(t, stripped.Get("use_path_style"))
	assert.Empty(t, stripped.Get("disable_https"))
}

func TestStripS3BlobParams_S3ForcePathStyleAlias(t *testing.T) {
	values := url.Values{}
	values.Set("s3ForcePathStyle", "true")

	_, options := stripS3BlobParams(values)

	assert.True(t, options.usePathStyle, "s3ForcePathStyle must alias to use_path_style")
}

func TestStripS3BlobParams_OnlyDisableHTTPS_LeavesOthersZero(t *testing.T) {
	values := url.Values{}
	values.Set("disable_https", "true")

	stripped, options := stripS3BlobParams(values)

	assert.True(t, options.disableHTTPS)
	assert.False(t, options.accelerate)
	assert.False(t, options.usePathStyle)
	assert.Empty(t, options.encryptionType)
	assert.Empty(t, options.kmsKeyID)
	assert.Empty(t, stripped.Get("disable_https"), "consumed param must not pass through")
}

func TestStripS3BlobParams_OnlyAccelerate_LeavesOthersZero(t *testing.T) {
	values := url.Values{}
	values.Set("accelerate", "true")

	stripped, options := stripS3BlobParams(values)

	assert.True(t, options.accelerate)
	assert.False(t, options.disableHTTPS)
	assert.False(t, options.usePathStyle)
	assert.Empty(t, stripped.Get("accelerate"))
}

func TestStripS3BlobParams_OnlyEncryption_PassesAesKeysIntoOptions(t *testing.T) {
	values := url.Values{}
	values.Set("ssetype", "AES256")
	values.Set("kmskeyid", "alias/my-key")

	stripped, options := stripS3BlobParams(values)

	assert.Equal(t, "AES256", options.encryptionType)
	assert.Equal(t, "alias/my-key", options.kmsKeyID)
	assert.Empty(t, stripped.Get("ssetype"))
	assert.Empty(t, stripped.Get("kmskeyid"))
}

func TestStripS3BlobParams_UnknownParams_PassThroughUntouched(t *testing.T) {
	values := url.Values{}
	values.Set("region", "us-east-1")
	values.Set("profile", "archive")
	values.Set("hostname_immutable", "true")

	stripped, options := stripS3BlobParams(values)

	assert.False(t, options.accelerate)
	assert.False(t, options.usePathStyle)
	assert.False(t, options.disableHTTPS)
	assert.Equal(t, "us-east-1", stripped.Get("region"))
	assert.Equal(t, "archive", stripped.Get("profile"))
	assert.Equal(t, "true", stripped.Get("hostname_immutable"))
}

func TestStripS3BlobParams_NonTrueAccelerate_TreatedAsFalse(t *testing.T) {
	values := url.Values{}
	values.Set("accelerate", "yes")

	stripped, options := stripS3BlobParams(values)

	assert.False(t, options.accelerate, "any non-'true' value reads as disabled")
	assert.Empty(t, stripped.Get("accelerate"), "key is still consumed regardless of value")
}

func TestS3Opener_AWSConfigFromURL_OverridesCredentialsWhenProvided(t *testing.T) {
	static := credentials.NewStaticCredentialsProvider("test-akid", "test-secret", "")
	opener := newS3Opener(Deps{Credentials: static})

	cfg, err := opener.awsConfigFromURL(context.Background(), url.Values{
		"region": {"us-east-1"},
	})
	require.NoError(t, err)

	creds, err := cfg.Credentials.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "test-akid", creds.AccessKeyID)
	assert.Equal(t, "test-secret", creds.SecretAccessKey)
}

func TestS3Opener_AWSConfigFromURL_LeavesGocloudDefaultWhenDepsEmpty(t *testing.T) {
	reference, err := gcaws.V2ConfigFromURLParams(context.Background(), url.Values{
		"region": {"us-east-1"},
	})
	require.NoError(t, err)

	opener := newS3Opener(Deps{})
	actual, err := opener.awsConfigFromURL(context.Background(), url.Values{
		"region": {"us-east-1"},
	})
	require.NoError(t, err)

	// With no override, the helper returns the gocloud-built config
	// untouched. Type equality on Credentials is the strongest stable
	// assertion we can make: a fresh provider instance per call rules
	// out pointer equality, but the provider TYPE matches when no
	// override is applied. The override-set sibling above guarantees
	// that "type matches" rules out the static-credentials override.
	assert.IsType(t, reference.Credentials, actual.Credentials,
		"empty Deps must leave cfg.Credentials at gocloud's default provider")
}
