package main

import (
	"strings"
	"testing"
)

func TestEncodeArg_AbsolutePath(t *testing.T) {
	out, err := encodeArg("/Users/demo/old-project")
	if err != nil {
		t.Fatalf("encodeArg returned error: %v", err)
	}
	if !strings.HasSuffix(out, "-Users-demo-old-project") {
		t.Fatalf("expected suffix -Users-demo-old-project, got %q", out)
	}
}

func TestEncodeArg_EmptyRejected(t *testing.T) {
	_, err := encodeArg("")
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}
