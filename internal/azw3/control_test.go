package azw3

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestFCISUsesCanonicalKF8Layout(t *testing.T) {
	record := buildFCIS(12345)
	if len(record) != 52 {
		t.Fatalf("FCIS length = %d, want 52", len(record))
	}
	wants := map[int]uint32{
		4: 0x14, 8: 0x10, 12: 2, 16: 0, 20: 12345,
		24: 0, 28: 0x28, 32: 0, 36: 0x28, 40: 8,
		44: 0x00010001, 48: 0,
	}
	for off, want := range wants {
		if got := binary.BigEndian.Uint32(record[off : off+4]); got != want {
			t.Fatalf("FCIS[%d] = %#x, want %#x", off, got, want)
		}
	}
}

func TestBuildRecordsAddsCalibreTextAreaPaddingRecord(t *testing.T) {
	var compiled compiledBook
	var remainder int
	for textLength := 1; textLength <= 32; textLength++ {
		book := normalizeBook(ebook.Book{
			Metadata: ebook.Metadata{Title: "Padding", Language: "en"},
			Spine: []ebook.Document{{
				Href: "text.xhtml",
				Body: ebook.Element("body", nil,
					ebook.Element("p", nil, ebook.Text(string(bytes.Repeat([]byte{'a'}, textLength)))),
				),
			}},
		})
		var err error
		compiled, err = compileBook(book)
		if err != nil {
			t.Fatalf("compileBook: %v", err)
		}

		compressedTextSize := 0
		for _, chunk := range compiled.records {
			compressedTextSize += len(compressPalmDOC(chunk.data)) + len(chunk.overlap) + 1
		}
		remainder = compressedTextSize % 4
		if remainder != 0 {
			break
		}
	}
	if remainder == 0 {
		t.Fatal("failed to construct a fixture that needs text-area padding")
	}

	records, err := buildRecords(compiled)
	if err != nil {
		t.Fatalf("buildRecords: %v", err)
	}
	paddingIndex := 1 + len(compiled.records)
	wantFirstNonText := paddingIndex + 1
	if got := int(binary.BigEndian.Uint32(records[0].data[80:84])); got != wantFirstNonText {
		t.Fatalf("firstNonTextRecord = %d, want %d", got, wantFirstNonText)
	}
	if got, want := records[paddingIndex].data, make([]byte, remainder); !bytes.Equal(got, want) {
		t.Fatalf("padding record = %x, want %x", got, want)
	}
	if got := records[wantFirstNonText].data; len(got) < 4 || string(got[:4]) != "INDX" {
		t.Fatalf("first non-text record does not start with INDX: %x", got)
	}
}

func TestLanguageCodeDistinguishesSimplifiedChineseLocale(t *testing.T) {
	tests := []struct {
		language string
		want     uint32
	}{
		{language: "zh-CN", want: 0x0804},
		{language: "zh", want: 0x0004},
	}
	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			if got := languageCode(tt.language); got != tt.want {
				t.Fatalf("languageCode(%q) = %#x, want %#x", tt.language, got, tt.want)
			}
		})
	}
}
