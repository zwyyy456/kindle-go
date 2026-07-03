package vocab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigAppliesDefaultsAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kindle2flashdict.toml")
	if err := os.WriteFile(path, []byte(`
[kindle]
vocab_db = "/tmp/vocab.db"

[sense_source]
socket_path = "/tmp/flashdict.sock"

[ai]
batch_size = 12
min_confidence = 0.6

[output]
path = "cards.json"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Kindle.VocabDB != "/tmp/vocab.db" {
		t.Fatalf("VocabDB = %q", cfg.Kindle.VocabDB)
	}
	if cfg.SenseSource.Type != "flashdict-lookup-bridge" {
		t.Fatalf("SenseSource.Type = %q", cfg.SenseSource.Type)
	}
	if cfg.SenseSource.SocketPath != "/tmp/flashdict.sock" {
		t.Fatalf("SenseSource.SocketPath = %q", cfg.SenseSource.SocketPath)
	}
	if cfg.AI.BatchSize != 12 {
		t.Fatalf("AI.BatchSize = %d", cfg.AI.BatchSize)
	}
	if cfg.AI.MinConfidence != 0.6 {
		t.Fatalf("AI.MinConfidence = %f", cfg.AI.MinConfidence)
	}
	if cfg.Output.Path != "cards.json" {
		t.Fatalf("Output.Path = %q", cfg.Output.Path)
	}
	if cfg.Output.ReviewPath != "review.jsonl" {
		t.Fatalf("Output.ReviewPath = %q", cfg.Output.ReviewPath)
	}
}

func TestValidateConfigRejectsMissingKindleDB(t *testing.T) {
	cfg := Config{}
	cfg.SenseSource.Type = "flashdict-lookup-bridge"
	cfg.SenseSource.SocketPath = "/tmp/flashdict.sock"
	cfg.SenseSource.DictionaryPolicy = "first-extractable-enabled"
	cfg.AI.Backend = "codex-exec"
	cfg.AI.BatchSize = 1
	cfg.AI.MinConfidence = 1
	cfg.Output.Type = "flashdict-json"

	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig returned nil")
	}
}
