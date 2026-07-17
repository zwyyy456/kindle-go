package proofread

import (
	"strings"
	"testing"
)

func TestTailWriterRetainsOnlyConfiguredSuffix(t *testing.T) {
	writer := newTailWriter(5)
	_, _ = writer.Write([]byte("abc"))
	_, _ = writer.Write([]byte("defgh"))
	if writer.String() != "defgh" {
		t.Fatalf("tail = %q", writer.String())
	}
	_, _ = writer.Write([]byte(strings.Repeat("x", 8)))
	if writer.String() != "xxxxx" {
		t.Fatalf("long tail = %q", writer.String())
	}
}
