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
	for _, want := range []string{"EXTH", "测试书", "作者", "zh-CN"} {
		if !bytes.Contains(records[0], []byte(want)) {
			t.Fatalf("header record missing %q", want)
		}
	}
	if !bytes.Contains(bytes.Join(records[1:int(inspectMOBIHeader(t, records[0]).firstNonText)], nil), []byte("正文。")) {
		t.Fatalf("text record missing paragraph: %q", records[1])
	}
	header := inspectMOBIHeader(t, records[0])
	if header.headerLength != 264 {
		t.Fatalf("mobi header length = %d", header.headerLength)
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
	for i := 1; i < int(header.firstNonText); i++ {
		if !utf8.Valid(records[i]) {
			t.Fatalf("text record %d is not valid utf-8", i)
		}
	}
}

type mobiHeader struct {
	headerLength uint32
	firstNonText uint32
	fdstRecord   uint32
	flisRecord   uint32
	fcisRecord   uint32
	ncxIndex     uint32
	chunkIndex   uint32
	skelIndex    uint32
	guideIndex   uint32
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
		headerLength: u32(20),
		firstNonText: u32(80),
		fdstRecord:   u32(192),
		fcisRecord:   u32(200),
		flisRecord:   u32(208),
		ncxIndex:     u32(244),
		chunkIndex:   u32(248),
		skelIndex:    u32(252),
		guideIndex:   u32(260),
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
