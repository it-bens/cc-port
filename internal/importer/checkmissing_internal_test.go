package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
)

func TestMergeResolutions_MergesManifestResolveValue(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{HOST}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{HOST}}", Resolve: "/srv/host"}},
	}}}

	resolutions, err := MergeResolutions(block, fromManifest, nil)

	require.NoError(t, err)
	assert.Equal(t, "/srv/host", resolutions["{{HOST}}"])
}

func TestMergeResolutions_IgnoresEmptyManifestResolveValue(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{ORG}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{ORG}}", Resolve: ""}},
	}}}

	resolutions, err := MergeResolutions(block, fromManifest, nil)

	require.NoError(t, err)
	_, has := resolutions["{{ORG}}"]
	assert.False(t, has, "an empty manifest Resolve value must not populate the merged map")
}

// buildEntriesWithBody returns the single archive.RawEntry produced by
// archiving one "<toolName>/<name>" file holding body, mirroring how
// archive.OpenReader + RawEntries hand entries to checkMissingResolutions in
// the real import path.
func buildEntriesWithBody(t *testing.T, toolName, name, body string) []archive.RawEntry {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entryWriter, err := writer.Create(toolName + "/" + name)
	require.NoError(t, err)
	_, err = entryWriter.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	reader, err := archive.OpenReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)
	return entries
}

// TestCheckMissingResolutions_RefusesUnresolvedDeclaredKeyPresentInBody covers
// both the "declared key embedded in a body lacks a resolution" preflight
// refusal and the "unresolved non-implicit key" error contract: a declared,
// non-implicit, unresolved key that is actually referenced in an archive body
// must produce a MissingResolutionsError naming it.
func TestCheckMissingResolutions_RefusesUnresolvedDeclaredKeyPresentInBody(t *testing.T) {
	const (
		projectPathKey = "{{PROJECT_PATH}}"
		homePathKey    = "{{HOME}}"
		nonImplicitKey = "{{CUSTOM_KEY}}"
	)
	block := manifest.Tool{
		Name: "claude",
		Placeholders: []manifest.Placeholder{
			{Key: projectPathKey, Original: "/Users/example/project"},
			{Key: homePathKey, Original: "/Users/example"},
			{Key: nonImplicitKey, Original: "/some/path"},
		},
	}
	anchors := map[string]string{
		projectPathKey: "/Users/recipient/project",
		homePathKey:    "/Users/recipient",
	}
	resolutions := map[string]string{
		projectPathKey: "/Users/recipient/project",
		homePathKey:    "/Users/recipient",
		// nonImplicitKey deliberately omitted.
	}
	entries := buildEntriesWithBody(t, "claude", "note.txt", "references "+nonImplicitKey+" here")

	err := checkMissingResolutions(
		context.Background(), "claude", block, anchors, resolutions, entries, archive.DefaultCaps().MaxAggregateBytes,
	)

	require.Error(t, err)
	var missingErr *MissingResolutionsError
	require.ErrorAs(t, err, &missingErr)
	assert.Equal(t, []string{nonImplicitKey}, missingErr.Keys)
}

// TestUnresolvedReferencedKeys covers the classification that makes pull
// planning and import preflight agree (finding FE3): a declared key is
// flagged only when it is BOTH unresolved AND actually referenced in an
// archive body. A declared-but-unreferenced key never blocks the archive's
// write path (nothing needs its value), and a resolved key never needs
// classifying at all.
func TestUnresolvedReferencedKeys(t *testing.T) {
	const key = "{{PLACEHOLDER}}"

	tests := []struct {
		name        string
		anchors     map[string]string
		resolutions map[string]string
		body        string
		wantFlagged bool
	}{
		{
			name:        "declared, referenced, unresolved is flagged",
			body:        "references " + key + " here",
			wantFlagged: true,
		},
		{
			name:        "declared, unreferenced is not flagged",
			body:        "no reference to the token here",
			wantFlagged: false,
		},
		{
			name:        "declared, referenced, resolved is not flagged",
			resolutions: map[string]string{key: "/recipient/resolved"},
			body:        "references " + key + " here",
			wantFlagged: false,
		},
		{
			name:        "declared, referenced, implicit anchor is not flagged",
			anchors:     map[string]string{key: "/recipient/implicit"},
			body:        "references " + key + " here",
			wantFlagged: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			block := manifest.Tool{
				Name:         "claude",
				Placeholders: []manifest.Placeholder{{Key: key, Original: "/some/path"}},
			}
			entries := buildEntriesWithBody(t, "claude", "note.txt", testCase.body)

			missing, err := UnresolvedReferencedKeys(
				context.Background(), block, testCase.anchors, testCase.resolutions, entries, archive.DefaultCaps().MaxAggregateBytes,
			)

			require.NoError(t, err)
			if testCase.wantFlagged {
				assert.Equal(t, []string{key}, missing)
			} else {
				assert.Empty(t, missing)
			}
		})
	}
}

// TestUnresolvedReferencedKeys_CancelledContextOnZeroCandidatesReturnsError
// pins that a canceled context is never masked as "nothing unresolved":
// when every declared key is already resolved (or implicit), the
// candidate-key list is empty and archive.ClassifyPresentKeys is never
// called at all, so this path depends entirely on the entry-time check.
func TestUnresolvedReferencedKeys_CancelledContextOnZeroCandidatesReturnsError(t *testing.T) {
	const key = "{{PLACEHOLDER}}"
	block := manifest.Tool{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: key, Original: "/some/path"}},
	}
	resolutions := map[string]string{key: "/recipient/resolved"}
	entries := buildEntriesWithBody(t, "claude", "note.txt", "no reference here")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	missing, err := UnresolvedReferencedKeys(ctx, block, nil, resolutions, entries, archive.DefaultCaps().MaxAggregateBytes)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, missing, "a canceled context must not return a plausible-looking empty result")
}

// TestUnresolvedReferencedKeys_ExcludesFileHistoryBodies pins that a
// declared key appearing only inside a Claude file-history snapshot is
// never flagged, while the identical key appearing in a normal body still
// is. cc-port never inspects or rewrites file-history snapshot bytes
// (docs/architecture.md §File-history policy), and Stage never substitutes
// placeholders into them either (see claude.Workspace.Stage's
// "file-history/" case), so a token that appears only inside an opaque
// snapshot is never rewritten on import regardless of whether it is
// resolved.
//
// The exclusion is scoped to entry.ToolName == "claude": only Claude owns
// a file-history category, so a same-shaped path under a different tool
// (here, a hypothetical "codex/file-history/...") must not be excluded —
// that would embed Claude-specific routing into a classifier every tool
// shares.
func TestUnresolvedReferencedKeys_ExcludesFileHistoryBodies(t *testing.T) {
	const key = "{{PLACEHOLDER}}"

	tests := []struct {
		name        string
		toolName    string
		entryName   string
		wantFlagged bool
	}{
		{
			name:        "referenced only in a Claude file-history snapshot is not flagged",
			toolName:    "claude",
			entryName:   "file-history/session/hash@v1",
			wantFlagged: false,
		},
		{
			name:        "referenced in a normal Claude body is flagged",
			toolName:    "claude",
			entryName:   "note.txt",
			wantFlagged: true,
		},
		{
			name:        "a same-shaped file-history path under a different tool is still flagged",
			toolName:    "codex",
			entryName:   "file-history/session/hash@v1",
			wantFlagged: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			block := manifest.Tool{
				Name:         "claude",
				Placeholders: []manifest.Placeholder{{Key: key, Original: "/some/path"}},
			}
			entries := buildEntriesWithBody(t, testCase.toolName, testCase.entryName, "references "+key+" here")

			missing, err := UnresolvedReferencedKeys(context.Background(), block, nil, nil, entries, archive.DefaultCaps().MaxAggregateBytes)

			require.NoError(t, err)
			if testCase.wantFlagged {
				assert.Equal(t, []string{key}, missing)
			} else {
				assert.Empty(t, missing, "a key referenced only inside a file-history snapshot must not be flagged")
			}
		})
	}
}
