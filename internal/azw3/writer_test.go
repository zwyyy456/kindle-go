package azw3_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/azw3"
	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestWritePalmDBMOBIWithMetadataTextAndTOC(t *testing.T) {
	out := filepath.Join(t.TempDir(), "book.azw3")
	b := ebook.Book{
		Metadata: ebook.Metadata{Title: "测试书", Author: "作者", Language: "zh-CN"},
		Spine: []ebook.Document{{
			Href:  "text/chapter-001.xhtml",
			Title: "第一章",
			Body: ebook.Element("body", nil,
				ebook.Element("section", []ebook.Attr{ebook.A("id", "chapter-001")},
					ebook.Element("h2", []ebook.Attr{ebook.A("id", "heading-001")}, ebook.Text("第一章")),
					ebook.Element("p", nil, ebook.Text("正文。")),
				),
			),
		}},
		TOC: []ebook.TOCEntry{{Title: "第一章", Href: "text/chapter-001.xhtml#heading-001"}},
	}
	if err := azw3.Write(out, b, azw3.Options{}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 128 {
		t.Fatalf("file is too small: %d", len(data))
	}
	if string(data[60:64]) != "BOOK" || string(data[64:68]) != "MOBI" {
		t.Fatalf("palm type/creator = %q/%q", data[60:64], data[64:68])
	}
	records := readRecords(t, data)
	if len(records) < 3 {
		t.Fatalf("record count = %d", len(records))
	}
	if !bytes.Contains(records[0], []byte("MOBI")) {
		t.Fatal("header record missing MOBI")
	}
	for _, want := range []string{"EXTH", "测试书", "作者", "zh-CN"} {
		if !bytes.Contains(records[0], []byte(want)) {
			t.Fatalf("header record missing %q", want)
		}
	}
	if !bytes.Contains(records[1], []byte("<p>正文。</p>")) {
		t.Fatalf("text record missing paragraph: %q", records[1])
	}
	if !bytes.HasPrefix(records[len(records)-1], []byte("INDX\n")) {
		t.Fatalf("last record is not TOC index: %q", records[len(records)-1][:min(8, len(records[len(records)-1]))])
	}
	if !bytes.Contains(records[len(records)-1], []byte("text/chapter-001.xhtml#heading-001")) {
		t.Fatal("TOC record missing chapter href")
	}
}

func readRecords(t *testing.T, data []byte) [][]byte {
	t.Helper()
	count := int(binary.BigEndian.Uint16(data[76:78]))
	if count == 0 {
		t.Fatal("record count is zero")
	}
	offsets := make([]int, count+1)
	for i := 0; i < count; i++ {
		pos := 78 + i*8
		offsets[i] = int(binary.BigEndian.Uint32(data[pos : pos+4]))
	}
	offsets[count] = len(data)
	records := make([][]byte, count)
	for i := 0; i < count; i++ {
		if offsets[i] < 0 || offsets[i] > offsets[i+1] || offsets[i+1] > len(data) {
			t.Fatalf("bad record offsets %d: %d..%d", i, offsets[i], offsets[i+1])
		}
		records[i] = data[offsets[i]:offsets[i+1]]
	}
	return records
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
