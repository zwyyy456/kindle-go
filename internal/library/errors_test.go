package library

import (
	"errors"
	"fmt"
	"testing"
)

func TestSourceErrorCodeUsesTypedErrors(t *testing.T) {
	if got := SourceErrorCode(fmt.Errorf("source_hash_mismatch: stale message")); got != "" {
		t.Fatalf("untyped error code = %q", got)
	}
	if got := SourceErrorCode(fmt.Errorf("resolve input: %w", ErrSourceChanged)); got != "source_hash_mismatch" {
		t.Fatalf("changed source code = %q", got)
	}
	if got := SourceErrorCode(fmt.Errorf("resolve input: %w", ErrSourceUnavailable)); got != "source_unavailable" {
		t.Fatalf("unavailable source code = %q", got)
	}
	if got := SourceErrorCode(errors.New("database unavailable")); got != "" {
		t.Fatalf("infrastructure error code = %q", got)
	}
}
