package converter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
)

func TestValidateFormatCombinations(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{"txt to epub", Request{InputPath: "in.txt", OutputPath: "out.epub", InputFormat: FormatTXT, OutputFormat: FormatEPUB}, false},
		{"txt to azw3", Request{InputPath: "in.txt", OutputPath: "out.azw3", InputFormat: FormatTXT, OutputFormat: FormatAZW3}, false},
		{"epub to azw3", Request{InputPath: "in.epub", OutputPath: "out.azw3", InputFormat: FormatEPUB, OutputFormat: FormatAZW3}, false},
		{"epub to epub", Request{InputPath: "in.epub", OutputPath: "out.epub", InputFormat: FormatEPUB, OutputFormat: FormatEPUB}, true},
		{"wrong extension", Request{InputPath: "in.txt", OutputPath: "out.epub", InputFormat: FormatTXT, OutputFormat: FormatAZW3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Convert error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConvertTXTToEPUB(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "book.txt")
	output := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(input, []byte("第1章 开始\n正文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := txtconfig.Defaults()
	_, err := Convert(context.Background(), Request{InputPath: input, OutputPath: output, InputFormat: FormatTXT, OutputFormat: FormatEPUB, TXTConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
}
