package importer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
)

func TestResolvePlaceholders(t *testing.T) {
	content := []byte("path is __PROJECT_PATH__ and again __PROJECT_PATH__")
	resolutions := map[string]string{
		"__PROJECT_PATH__": "/home/user/myproject",
	}

	result := importer.ResolvePlaceholders(content, resolutions)

	assert.Equal(t, []byte("path is /home/user/myproject and again /home/user/myproject"), result)
}

func TestResolvePlaceholders_UnresolvedLeft(t *testing.T) {
	content := []byte("known: __KNOWN__ unknown: __UNKNOWN__")
	resolutions := map[string]string{
		"__KNOWN__": "/home/user/known",
	}

	result := importer.ResolvePlaceholders(content, resolutions)

	assert.Equal(t, []byte("known: /home/user/known unknown: __UNKNOWN__"), result)
}

func TestValidateResolutions(t *testing.T) {
	tests := []struct {
		name        string
		resolutions map[string]string
		wantErr     bool
	}{
		{
			name: "valid absolute paths",
			resolutions: map[string]string{
				"{{PLACEHOLDER}}": "/home/user/project",
			},
			wantErr: false,
		},
		{
			name: "empty value is always rejected",
			resolutions: map[string]string{
				"{{PLACEHOLDER}}": "",
			},
			wantErr: true,
		},
		{
			name: "relative path is rejected",
			resolutions: map[string]string{
				"{{PLACEHOLDER}}": "relative/path",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := importer.ValidateResolutions(testCase.resolutions)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestClassifyPlaceholders(t *testing.T) {
	for _, testCase := range classifyPlaceholderCases() {
		t.Run(testCase.name, func(t *testing.T) {
			missing, undeclared := importer.ClassifyPlaceholders(
				testCase.bodies, testCase.declared, testCase.resolutions,
			)
			assert.Equal(t, testCase.wantMissing, missing)
			assert.Equal(t, testCase.wantUndeclared, undeclared)
		})
	}
}

// classifyPlaceholderCases returns the table feeding TestClassifyPlaceholders.
// Kept as a helper so the test body stays under funlen while all cases live
// in one place.
func classifyPlaceholderCases() []classifyCase {
	cases := classifyUpperSnakeCases()
	cases = append(cases, classifyExoticShapeCases()...)
	return cases
}

func classifyUpperSnakeCases() []classifyCase {
	trueVal := true
	falseVal := false
	return []classifyCase{
		{
			name:   "all present keys resolved",
			bodies: [][]byte{[]byte(`cwd={{PROJECT_PATH}}, home={{HOME}}`)},
			declared: []manifest.Placeholder{
				{Key: "{{PROJECT_PATH}}", Resolvable: &trueVal},
				{Key: "{{HOME}}", Resolvable: &trueVal},
			},
			resolutions: map[string]string{
				"{{PROJECT_PATH}}": "/dest/project",
				"{{HOME}}":         "/dest",
			},
		},
		{
			name:     "declared Resolvable=false + present + not resolved is clean",
			bodies:   [][]byte{[]byte(`legacy={{UNRESOLVABLE}}`)},
			declared: []manifest.Placeholder{{Key: "{{UNRESOLVABLE}}", Resolvable: &falseVal}},
		},
		{
			name:        "declared nil + not resolved is missing",
			bodies:      [][]byte{[]byte(`extra={{EXTRA}}`)},
			declared:    []manifest.Placeholder{{Key: "{{EXTRA}}", Resolvable: nil}},
			wantMissing: []string{"{{EXTRA}}"},
		},
		{
			name:        "declared true + not resolved is missing",
			bodies:      [][]byte{[]byte(`extra={{EXTRA}}`)},
			declared:    []manifest.Placeholder{{Key: "{{EXTRA}}", Resolvable: &trueVal}},
			wantMissing: []string{"{{EXTRA}}"},
		},
		{
			name:           "present but not declared is undeclared",
			bodies:         [][]byte{[]byte(`leaked={{SECRET}}`)},
			declared:       []manifest.Placeholder{{Key: "{{PROJECT_PATH}}", Resolvable: &trueVal}},
			wantUndeclared: []string{"{{SECRET}}"},
		},
		{
			name:   "both missing and undeclared populated and sorted",
			bodies: [][]byte{[]byte(`{{ZETA}} {{ALPHA}} {{UNDECLARED_B}} {{UNDECLARED_A}}`)},
			declared: []manifest.Placeholder{
				{Key: "{{ZETA}}", Resolvable: &trueVal},
				{Key: "{{ALPHA}}", Resolvable: &trueVal},
			},
			wantMissing:    []string{"{{ALPHA}}", "{{ZETA}}"},
			wantUndeclared: []string{"{{UNDECLARED_A}}", "{{UNDECLARED_B}}"},
		},
		{
			name:     "declared but absent from bodies is clean",
			bodies:   [][]byte{[]byte("no placeholder tokens here")},
			declared: []manifest.Placeholder{{Key: "{{UNUSED}}", Resolvable: &trueVal}},
		},
		{
			// importer.Run pre-fills PROJECT_PATH unconditionally.
			name:     "PROJECT_PATH resolved implicitly even without explicit resolution",
			bodies:   [][]byte{[]byte(`cwd={{PROJECT_PATH}}`)},
			declared: []manifest.Placeholder{{Key: "{{PROJECT_PATH}}", Resolvable: &trueVal}},
		},
	}
}

// classifyExoticShapeCases exercises manifest-driven resolution against key
// shapes the upper-snake scanner cannot see (punctuation, lowercase).
// Resolution is now driven by literal substring search over declared keys,
// so grammar does not affect classification.
func classifyExoticShapeCases() []classifyCase {
	trueVal := true
	falseVal := false
	return []classifyCase{
		{
			name:        "exotic-shape declared key missing a resolution is flagged",
			bodies:      [][]byte{[]byte(`path={{my-weird.key}}`)},
			declared:    []manifest.Placeholder{{Key: "{{my-weird.key}}", Resolvable: &trueVal}},
			wantMissing: []string{"{{my-weird.key}}"},
		},
		{
			name:   "exotic-shape declared key with a resolution is clean",
			bodies: [][]byte{[]byte(`path={{my-weird.key}}`)},
			declared: []manifest.Placeholder{
				{Key: "{{my-weird.key}}", Resolvable: &trueVal},
			},
			resolutions: map[string]string{"{{my-weird.key}}": "/dest/weird"},
		},
		{
			name:     "exotic-shape declared key marked Resolvable=false is clean",
			bodies:   [][]byte{[]byte(`legacy={{my-weird.key}}`)},
			declared: []manifest.Placeholder{{Key: "{{my-weird.key}}", Resolvable: &falseVal}},
		},
	}
}

// classifyCase captures one expected outcome of ClassifyPlaceholders for the
// table-driven TestClassifyPlaceholders.
type classifyCase struct {
	name           string
	bodies         [][]byte
	declared       []manifest.Placeholder
	resolutions    map[string]string
	wantMissing    []string
	wantUndeclared []string
}

func TestCheckConflict(t *testing.T) {
	t.Run("no conflict when directory does not exist", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")

		err := importer.CheckConflict(nonExistentDir)

		assert.NoError(t, err)
	})

	t.Run("conflict when directory exists", func(t *testing.T) {
		existingDir := t.TempDir()
		require.DirExists(t, existingDir)

		err := importer.CheckConflict(existingDir)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}
