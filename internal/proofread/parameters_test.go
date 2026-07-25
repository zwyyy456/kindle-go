package proofread

import (
	"strings"
	"testing"
)

func TestDecodeTaskParameterVersions(t *testing.T) {
	legacy, err := decodeProofreadParameters(`{"format":"txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SchemaVersion != taskParametersVersion {
		t.Fatalf("legacy schema version = %d", legacy.SchemaVersion)
	}
	if _, err := decodeRevisionParameters(`{"version":2,"future":true}`); err == nil || !strings.Contains(err.Error(), "unsupported revision task parameters version 2") {
		t.Fatalf("newer version error = %v", err)
	}
	if _, err := decodeProofreadParameters(`{"version":1,"unexpected":true}`); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}
