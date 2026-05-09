package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopBannerRenderWritesNothing(t *testing.T) {
	var buffer bytes.Buffer

	require.NoError(t, noopBanner{}.Render(&buffer))

	assert.Empty(t, buffer.Bytes())
}

func TestNoopBannerRenderBesideWritesTextOnly(t *testing.T) {
	var buffer bytes.Buffer

	require.NoError(t, noopBanner{}.RenderBeside(&buffer, "cc-port 1.2.3\n"))

	assert.Equal(t, "cc-port 1.2.3\n", buffer.String())
}

func TestNoopBannerBesideStringReturnsTextUnchanged(t *testing.T) {
	var buffer bytes.Buffer

	got := noopBanner{}.BesideString(&buffer, "cc-port 1.2.3\n")

	assert.Equal(t, "cc-port 1.2.3\n", got)
}
