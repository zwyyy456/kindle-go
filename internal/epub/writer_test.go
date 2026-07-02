package epub

import (
	"archive/zip"
	"path/filepath"
	"testing"

	"github.com/zwyyy/txt2epub/internal/book"
	"github.com/zwyyy/txt2epub/internal/config"
)

func TestWriteEPUBStructure(t *testing.T) {
	out := filepath.Join(t.TempDir(), "book.epub")
	b := book.Book{
		Title:    "测试书",
		Author:   "作者",
		Language: "zh-CN",
		Cover:    true,
		Style:    config.Defaults().Style,
		Sections: []book.Section{{
			ID:    "chapter-001",
			Title: "第一章 开始",
			Level: 2,
			Blocks: []book.Block{
				{Kind: book.BlockHeading, Level: 2, Text: "第一章 开始", ID: "heading-001"},
				{Kind: book.BlockParagraph, Text: "正文。"},
			},
		}},
		Headings: []book.Heading{{ID: "heading-001", Title: "第一章 开始", Level: 2, SectionID: "chapter-001"}},
	}
	if err := Write(out, b); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
		t.Fatalf("mimetype is not the first zip entry")
	}
	if zr.File[0].Method != zip.Store {
		t.Fatalf("mimetype is compressed")
	}

	required := map[string]bool{
		"mimetype":                     false,
		"META-INF/container.xml":       false,
		"OEBPS/content.opf":            false,
		"OEBPS/nav.xhtml":              false,
		"OEBPS/toc.ncx":                false,
		"OEBPS/cover.xhtml":            false,
		"OEBPS/text/chapter-001.xhtml": false,
	}
	for _, f := range zr.File {
		if _, ok := required[f.Name]; ok {
			required[f.Name] = true
		}
	}
	for name, ok := range required {
		if !ok {
			t.Fatalf("missing %s", name)
		}
	}
}
