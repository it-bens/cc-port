package importer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
)

func TestBuildHistoryBytes_EmptyExistingPrependsNothing(t *testing.T) {
	appends := [][]byte{[]byte("line1\n"), []byte("line2\n")}
	got := importer.BuildHistoryBytes(nil, appends)
	assert.Equal(t, []byte("line1\nline2\n"), got)
}

func TestBuildHistoryBytes_ExistingEndsWithNewlineKeepsSingleSeparator(t *testing.T) {
	existing := []byte("line1\n")
	appends := [][]byte{[]byte("line2\n")}
	got := importer.BuildHistoryBytes(existing, appends)
	assert.Equal(t, []byte("line1\nline2\n"), got)
}

func TestBuildHistoryBytes_ExistingMissingTrailingNewlineInsertsOne(t *testing.T) {
	existing := []byte("line1")
	appends := [][]byte{[]byte("line2\n")}
	got := importer.BuildHistoryBytes(existing, appends)
	assert.Equal(t, []byte("line1\nline2\n"), got)
}

func TestBuildHistoryBytes_MultipleAppendsConcatInOrder(t *testing.T) {
	existing := []byte("a\n")
	appends := [][]byte{[]byte("b\n"), []byte("c\n"), []byte("d\n")}
	got := importer.BuildHistoryBytes(existing, appends)
	assert.Equal(t, []byte("a\nb\nc\nd\n"), got)
}

func TestMergeProjectConfigBytes_EmptyExistingStartsFromObject(t *testing.T) {
	block := []byte(`{"setting":1}`)
	got, err := importer.MergeProjectConfigBytes(nil, "/fake/config", "/proj", block)
	require.NoError(t, err)
	assert.JSONEq(t, `{"projects":{"/proj":{"setting":1}}}`, string(got))
}

func TestMergeProjectConfigBytes_PreservesSiblingKeys(t *testing.T) {
	existing := []byte(`{"theme":"dark","projects":{"/other":{"x":1}}}`)
	block := []byte(`{"setting":2}`)
	got, err := importer.MergeProjectConfigBytes(existing, "/fake/config", "/proj", block)
	require.NoError(t, err)
	assert.Contains(t, string(got), `"theme":"dark"`, "sibling top-level key must survive")
	assert.Contains(t, string(got), `"/other":{"x":1}`, "sibling project must survive")
	assert.Contains(t, string(got), `"/proj":{"setting":2}`, "new project must be present")
}

func TestMergeProjectConfigBytes_RejectsInvalidJSON(t *testing.T) {
	existing := []byte(`{not valid json`)
	_, err := importer.MergeProjectConfigBytes(existing, "/fake/config", "/proj", []byte(`{}`))
	var configErr *importer.InvalidConfigJSONError
	require.ErrorAs(t, err, &configErr)
	assert.Equal(t, "/fake/config", configErr.Path)
}
