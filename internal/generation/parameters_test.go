package generation

import (
	"strings"
	"testing"
)

func TestDecodeParametersVersions(t *testing.T) {
	legacy, err := decodeParameters(`{"input_format":"txt","output_format":"epub"}`)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SchemaVersion != taskParametersVersion {
		t.Fatalf("legacy schema version = %d", legacy.SchemaVersion)
	}
	if _, err := decodeParameters(`{"version":2,"future":true}`); err == nil || !strings.Contains(err.Error(), "unsupported generation task parameters version 2") {
		t.Fatalf("newer version error = %v", err)
	}
	if _, err := decodeParameters(`{"version":1,"unexpected":true}`); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}
