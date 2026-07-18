package importer

import (
	"archive/zip"
	"bytes"
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

	resolutions, err := mergeResolutions(block, fromManifest, nil)

	require.NoError(t, err)
	assert.Equal(t, "/srv/host", resolutions["{{HOST}}"])
}

func TestMergeResolutions_IgnoresEmptyManifestResolveValue(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{ORG}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{ORG}}", Resolve: ""}},
	}}}

	resolutions, err := mergeResolutions(block, fromManifest, nil)

	require.NoError(t, err)
	_, has := resolutions["{{ORG}}"]
	assert.False(t, has, "an empty manifest Resolve value must not populate the merged map")
}

// buildEntriesWithBody returns the single archive.RawEntry produced by
// archiving one "claude/<name>" file holding body, mirroring how
// archive.OpenReader + RawEntries hand entries to checkMissingResolutions in
// the real import path.
func buildEntriesWithBody(t *testing.T, name, body string) []archive.RawEntry {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entryWriter, err := writer.Create("claude/" + name)
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
	entries := buildEntriesWithBody(t, "note.txt", "references "+nonImplicitKey+" here")

	err := checkMissingResolutions("claude", block, anchors, resolutions, entries, archive.DefaultCaps().MaxAggregateBytes)

	require.Error(t, err)
	var missingErr *MissingResolutionsError
	require.ErrorAs(t, err, &missingErr)
	assert.Equal(t, []string{nonImplicitKey}, missingErr.Keys)
}
