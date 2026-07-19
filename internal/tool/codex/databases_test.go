package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSQLiteBusyCodeClassifiesPrimaryAndExtendedCodes(t *testing.T) {
	cases := []struct {
		name string
		code int
		want bool
	}{
		{name: "busy", code: 5, want: true},
		{name: "busy recovery", code: 261, want: true},
		{name: "busy snapshot", code: 517, want: true},
		{name: "constraint", code: 19, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, isSQLiteBusyCode(testCase.code))
		})
	}
}
