package azw3_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

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
	if got := binary.BigEndian.Uint16(records[0][0:2]); got != 2 {
		t.Fatalf("PalmDOC compression = %d, want 2", got)
	}
	for _, want := range []string{"EXTH", "测试书", "作者", "zh"} {
		if !bytes.Contains(records[0], []byte(want)) {
			t.Fatalf("header record missing %q", want)
		}
	}
	initialHeader := inspectMOBIHeader(t, records[0])
	if !bytes.Contains(decompressTextRecords(t, records[1:1+int(initialHeader.textRecordCount)]), []byte("正文。")) {
		t.Fatalf("text record missing paragraph: %q", records[1])
	}
	header := inspectMOBIHeader(t, records[0])
	if header.headerLength != 264 {
		t.Fatalf("mobi header length = %d", header.headerLength)
	}
	if header.languageCode != 0x0804 {
		t.Fatalf("MOBI language code = %#x, want zh-CN 0x0804", header.languageCode)
	}
	for name, idx := range map[string]uint32{
		"chunk": header.chunkIndex,
		"skel":  header.skelIndex,
		"guide": header.guideIndex,
		"ncx":   header.ncxIndex,
	} {
		if idx == 0xffffffff {
			t.Fatalf("%s index is null", name)
		}
		if int(idx) >= len(records) {
			t.Fatalf("%s index points past records: %d >= %d", name, idx, len(records))
		}
		record := records[int(idx)]
		if !bytes.HasPrefix(record, []byte("INDX")) {
			t.Fatalf("%s index record %d has prefix %q", name, idx, record[:min(8, len(record))])
		}
	}
	for name, idx := range map[string]uint32{
		"FDST": header.fdstRecord,
		"FLIS": header.flisRecord,
		"FCIS": header.fcisRecord,
	} {
		if int(idx) >= len(records) {
			t.Fatalf("%s points past records: %d >= %d", name, idx, len(records))
		}
		record := records[int(idx)]
		if !bytes.HasPrefix(record, []byte(name)) {
			t.Fatalf("%s record %d has prefix %q", name, idx, record[:min(8, len(record))])
		}
	}
	if !bytes.Contains(bytes.Join(records, nil), []byte("第一章")) {
		t.Fatal("index records missing TOC label")
	}
}

func TestWriteSplitsLargeChineseTextOnUTF8Boundaries(t *testing.T) {
	out := filepath.Join(t.TempDir(), "large.azw3")
	var paras []*ebook.Node
	for i := 0; i < 600; i++ {
		paras = append(paras, ebook.Element("p", nil, ebook.Text("中文正文很长，用来覆盖 record 拆分边界。")))
	}
	b := ebook.Book{
		Metadata: ebook.Metadata{Title: "大文件", Language: "zh-CN"},
		Spine: []ebook.Document{{
			Href:  "text/chapter-001.xhtml",
			Title: "第一章",
			Body:  ebook.Element("body", nil, ebook.Element("section", []ebook.Attr{ebook.A("id", "chapter-001")}, paras...)),
		}},
	}
	if err := azw3.Write(out, b, azw3.Options{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	records := readRecords(t, data)
	header := inspectMOBIHeader(t, records[0])
	if header.firstNonText <= 2 {
		t.Fatalf("expected multiple text records, first non-text = %d", header.firstNonText)
	}
	for i := 1; i <= int(header.textRecordCount); i++ {
		decoded := decompressTextRecords(t, records[i:i+1])
		if i < int(header.textRecordCount) && len(decoded) != 4096 {
			t.Fatalf("text record %d decoded length = %d, want 4096", i, len(decoded))
		}
	}
	if decoded := decompressTextRecords(t, records[1:1+int(header.textRecordCount)]); !utf8.Valid(decoded) {
		t.Fatal("reconstructed text is not valid UTF-8")
	}
	if header.extraDataFlags != 3 {
		t.Fatalf("extra data flags = %d, want multibyte and indexing flags", header.extraDataFlags)
	}
}

type mobiHeader struct {
	textRecordCount uint16
	headerLength    uint32
	firstNonText    uint32
	languageCode    uint32
	huffmanRecord   uint32
	extraDataFlags  uint16
	fdstRecord      uint32
	fdstCount       uint32
	flisRecord      uint32
	fcisRecord      uint32
	ncxIndex        uint32
	chunkIndex      uint32
	skelIndex       uint32
	guideIndex      uint32
}

func inspectMOBIHeader(t *testing.T, record []byte) mobiHeader {
	t.Helper()
	if len(record) < 280 {
		t.Fatalf("record 0 too short: %d", len(record))
	}
	if string(record[16:20]) != "MOBI" {
		t.Fatalf("record 0 missing MOBI: %q", record[16:20])
	}
	u32 := func(recordOffset int) uint32 {
		return binary.BigEndian.Uint32(record[recordOffset : recordOffset+4])
	}
	return mobiHeader{
		textRecordCount: binary.BigEndian.Uint16(record[8:10]),
		headerLength:    u32(20),
		firstNonText:    u32(80),
		languageCode:    u32(92),
		huffmanRecord:   u32(112),
		extraDataFlags:  binary.BigEndian.Uint16(record[242:244]),
		fdstRecord:      u32(192),
		fdstCount:       u32(196),
		fcisRecord:      u32(200),
		flisRecord:      u32(208),
		ncxIndex:        u32(244),
		chunkIndex:      u32(248),
		skelIndex:       u32(252),
		guideIndex:      u32(260),
	}
}

func TestKF8WritesReferencedStylesheetFlow(t *testing.T) {
	b := ebook.Book{
		Metadata: ebook.Metadata{Title: "Book", Language: "zh-CN"},
		Spine: []ebook.Document{{
			Href: "text.xhtml",
			Body: ebook.Element("body", nil,
				ebook.Element("p", nil, ebook.Text("text")),
			),
		}},
	}
	out := filepath.Join(t.TempDir(), "book.azw3")
	if err := azw3.Write(out, b, azw3.Options{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	records := readRecords(t, data)
	header := inspectMOBIHeader(t, records[0])
	if header.fdstCount < 2 {
		t.Fatalf("FDST flow count = %d, want at least main text and stylesheet flows", header.fdstCount)
	}
	fdst := records[header.fdstRecord]
	if len(fdst) < 12+int(header.fdstCount)*8 {
		t.Fatalf("FDST length = %d, want at least %d", len(fdst), 12+int(header.fdstCount)*8)
	}
	text := decompressTextRecords(t, records[1:1+int(header.textRecordCount)])
	mainEnd := binary.BigEndian.Uint32(fdst[16:20])
	styleStart := binary.BigEndian.Uint32(fdst[20:24])
	styleEnd := binary.BigEndian.Uint32(fdst[24:28])
	if mainEnd != styleStart || styleStart >= styleEnd || int(styleEnd) > len(text) {
		t.Fatalf("invalid main/style flow boundaries: mainEnd=%d style=%d..%d text=%d", mainEnd, styleStart, styleEnd, len(text))
	}
	mainFlow := text[:mainEnd]
	styleFlow := text[styleStart:styleEnd]
	if !bytes.Contains(mainFlow, []byte(`href="kindle:flow:0001?mime=text/css"`)) {
		t.Fatal("main flow does not reference the stylesheet flow")
	}
	if !bytes.Contains(styleFlow, []byte("text-indent")) {
		t.Fatalf("stylesheet flow does not contain book CSS: %q", styleFlow)
	}
}

func TestMOBIHeaderUsesZeroForAbsentHuffmanRecords(t *testing.T) {
	b := ebook.Book{Metadata: ebook.Metadata{Title: "Book"}, Spine: []ebook.Document{{Href: "text.xhtml", Body: ebook.Element("body", nil, ebook.Text("text"))}}}
	out := filepath.Join(t.TempDir(), "book.azw3")
	if err := azw3.Write(out, b, azw3.Options{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	header := inspectMOBIHeader(t, readRecords(t, data)[0])
	if header.huffmanRecord != 0 {
		t.Fatalf("Huffman record offset = %#x, want 0 when compression is PalmDOC", header.huffmanRecord)
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

func decompressTextRecords(t *testing.T, records [][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	for i, src := range records {
		indexingSize, markerSize := decodeBackwardSize(t, src)
		if indexingSize < markerSize || indexingSize > len(src) {
			t.Fatalf("text record %d has invalid indexing trailer size %d", i+1, indexingSize)
		}
		src = src[:len(src)-indexingSize]
		if len(src) == 0 {
			t.Fatalf("text record %d is missing multibyte trailer", i+1)
		}
		trailerSize := int(src[len(src)-1]&3) + 1
		if trailerSize > len(src) {
			t.Fatalf("text record %d has invalid multibyte trailer", i+1)
		}
		src = src[:len(src)-trailerSize]
		var decoded bytes.Buffer
		for pos := 0; pos < len(src); {
			b := src[pos]
			pos++
			switch {
			case b == 0 || (b >= 0x09 && b <= 0x7f):
				decoded.WriteByte(b)
			case b >= 1 && b <= 8:
				end := pos + int(b)
				if end > len(src) {
					t.Fatalf("text record %d has truncated literal run", i+1)
				}
				decoded.Write(src[pos:end])
				pos = end
			case b >= 0x80 && b <= 0xbf:
				if pos >= len(src) {
					t.Fatalf("text record %d has truncated back-reference", i+1)
				}
				code := uint16(b)<<8 | uint16(src[pos])
				pos++
				distance := int((code & 0x3ff8) >> 3)
				length := int(code&7) + 3
				for j := 0; j < length; j++ {
					data := decoded.Bytes()
					if distance == 0 || distance > len(data) {
						t.Fatalf("text record %d has invalid distance %d", i+1, distance)
					}
					decoded.WriteByte(data[len(data)-distance])
				}
			default:
				decoded.WriteByte(' ')
				decoded.WriteByte(b ^ 0x80)
			}
		}
		out.Write(decoded.Bytes())
	}
	return out.Bytes()
}

func decodeBackwardSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	value, shift := 0, 0
	for i := len(data) - 1; i >= 0; i-- {
		value |= int(data[i]&0x7f) << shift
		shift += 7
		if data[i]&0x80 != 0 {
			return value, (shift + 6) / 7
		}
	}
	t.Fatal("trailing data length marker is missing")
	return 0, 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
