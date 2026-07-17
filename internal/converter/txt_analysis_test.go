package converter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/epub"
)

func TestTXTPreviewAndGeneratedEPUBShareStructuredAnalysis(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "book.txt")
	if err := os.WriteFile(input, []byte("第一章 开始\n\n第一节 内容\n\n正文。\n\n第二章 继续\n\n后文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Metadata.Title = "结构一致性"
	analysis, err := AnalyzeTXT(input, cfg)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "book.epub")
	if _, err := Convert(context.Background(), Request{InputPath: input, OutputPath: output, InputFormat: FormatTXT, OutputFormat: FormatEPUB, TXTConfig: cfg}); err != nil {
		t.Fatal(err)
	}
	generated, err := epub.Read(output, epub.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Spine) != len(analysis.Book.Spine) || len(generated.TOC) != len(analysis.TOC) {
		t.Fatalf("generated structure = spine %d toc %d; preview = spine %d toc %d", len(generated.Spine), len(generated.TOC), len(analysis.Book.Spine), len(analysis.TOC))
	}
	for index := range analysis.TOC {
		if generated.TOC[index].Title != analysis.TOC[index].Title {
			t.Fatalf("toc[%d] = %q, want %q", index, generated.TOC[index].Title, analysis.TOC[index].Title)
		}
	}
}
