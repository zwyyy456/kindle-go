package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsVocabHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "vocab export") {
		t.Fatalf("help output = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunLegacyExportUsesProvidedConfigFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := RunLegacyExport([]string{"-config", "/tmp/missing-kindle2flashdict.toml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunLegacyExport returned nil")
	}
	if !strings.Contains(err.Error(), "/tmp/missing-kindle2flashdict.toml") {
		t.Fatalf("error = %v", err)
	}
}
