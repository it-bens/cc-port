package credentials

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadEnv_AllVarsSet_ReturnsAllValues(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAENV")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secretENV")
	t.Setenv("AWS_SESSION_TOKEN", "tokenENV")

	fields := readEnv()

	assert.Equal(t, "AKIAENV", fields.accessKeyID)
	assert.Equal(t, "secretENV", fields.secretAccessKey)
	assert.Equal(t, "tokenENV", fields.sessionToken)
}

func TestReadEnv_NoVarsSet_ReturnsZeroFields(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")

	fields := readEnv()

	assert.Empty(t, fields.accessKeyID)
	assert.Empty(t, fields.secretAccessKey)
	assert.Empty(t, fields.sessionToken)
}
