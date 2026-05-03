package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyResolutions(t *testing.T) {
	content := []byte("path is __PROJECT_PATH__ and again __PROJECT_PATH__")
	resolutions := map[string]string{
		"__PROJECT_PATH__": "/home/user/myproject",
	}

	result := applyResolutions(content, resolutions)

	assert.Equal(t, []byte("path is /home/user/myproject and again /home/user/myproject"), result)
}

func TestApplyResolutions_UnresolvedLeft(t *testing.T) {
	content := []byte("known: __KNOWN__ unknown: __UNKNOWN__")
	resolutions := map[string]string{
		"__KNOWN__": "/home/user/known",
	}

	result := applyResolutions(content, resolutions)

	assert.Equal(t, []byte("known: /home/user/known unknown: __UNKNOWN__"), result)
}
