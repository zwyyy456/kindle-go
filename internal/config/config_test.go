package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGroupedConfig(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.toml")
	data := `version = 1
[metadata]
title = "测试书"
language = "zh-CN"
[output]
format = "azw3"
cover = false
[txt]
split_level = 1
drop_regex = ["^广告"]
[style]
line_height = 1.8
[server]
web_addr = ":9000"
library_dir = "books"
`
	if err := os.WriteFile(filename, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metadata.Title != "测试书" || cfg.Output.Format != "azw3" || cfg.Output.Cover || cfg.TXT.SplitLevel != 1 || cfg.Server.WebAddr != ":9000" || cfg.Server.LibraryDir != "books" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadRejectsLegacyTopLevelFields(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "legacy.toml")
	if err := os.WriteFile(filename, []byte("version = 1\ntitle = \"legacy\"\nformat = \"azw3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(filename)
	if err == nil || !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsUnknownNestedField(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "unknown.toml")
	if err := os.WriteFile(filename, []byte("version = 1\n[output]\nformat = \"azw3\"\nprofile = \"pw3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(filename); err == nil {
		t.Fatal("Load accepted an unknown nested field")
	}
}

func TestLoadRejectsUnsupportedVersion(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "version.toml")
	if err := os.WriteFile(filename, []byte("version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(filename)
	if err == nil || !strings.Contains(err.Error(), "unsupported config version") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRequiresVersion(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "version.toml")
	if err := os.WriteFile(filename, []byte("[output]\nformat = \"azw3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(filename)
	if err == nil || !strings.Contains(err.Error(), "version is required") {
		t.Fatalf("Load error = %v", err)
	}
}
