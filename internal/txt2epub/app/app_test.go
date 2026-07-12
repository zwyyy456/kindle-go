package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/config"
)

func TestRunWritesAZW3WithoutCalibre(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "book.txt")
	output := filepath.Join(dir, "book.azw3")
	if err := os.WriteFile(input, []byte("第1章 开始\n正文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Output.Format = "azw3"
	cfg.Output.Path = output
	cfg.Metadata.Title = "测试书"
	cfg.Metadata.Author = "作者"
	var stdout bytes.Buffer
	if err := Run(input, cfg, Options{}, &stdout); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("MOBI")) {
		t.Fatal("output does not look like a MOBI/AZW3 file")
	}
}
