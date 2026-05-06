package remote

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
