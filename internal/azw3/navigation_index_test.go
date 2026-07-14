package azw3

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
	txtbook "github.com/flashdict/kindle2flashdict/internal/txt2epub/book"
)

func TestWriteNavigationIndexTargetsRealChunk(t *testing.T) {
	source := navigationIndexTestBook()
	normalized := normalizeBook(source)
	compiled, err := compileBook(normalized)
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "navigation.azw3")
	if err := Write(out, navigationIndexTestBook(), Options{}); err != nil {
		t.Fatal(err)
	}

	records := readAZW3Records(t, out)
	header := inspectAZW3MOBIHeader(t, records[0])
	text := decompressTextRecords(t, records[1:1+int(header.textRecordCount)])

	chunkIndex := inspectAZW3Index(t, records, header.chunkIndex)
	skelIndex := inspectAZW3Index(t, records, header.skelIndex)
	ncxIndex := inspectAZW3Index(t, records, header.ncxIndex)
	guideIndex := inspectAZW3Index(t, records, header.guideIndex)

	if len(chunkIndex.entries) != len(compiled.chunkTable) {
		t.Fatalf("chunk index entries = %d, compiled chunks = %d", len(chunkIndex.entries), len(compiled.chunkTable))
	}
	if len(skelIndex.entries) != len(compiled.skelTable) {
		t.Fatalf("skel index entries = %d, compiled skels = %d", len(skelIndex.entries), len(compiled.skelTable))
	}
	if len(ncxIndex.entries) != len(compiled.tocTable) {
		t.Fatalf("ncx entries = %d, compiled toc = %d", len(ncxIndex.entries), len(compiled.tocTable))
	}
	if len(guideIndex.entries) != len(compiled.guideTable) {
		t.Fatalf("guide entries = %d, compiled guide = %d", len(guideIndex.entries), len(compiled.guideTable))
	}

	for i, entry := range skelIndex.entries {
		want := compiled.skelTable[i]
		chunkCounts := requiredTag(t, entry, 1, "chunk_count")
		for _, got := range chunkCounts {
			if got != want.chunkCount {
				t.Fatalf("skel %d chunk_count = %v, want %d", i, chunkCounts, want.chunkCount)
			}
		}
		geometry := requiredTag(t, entry, 6, "skel geometry")
		if len(geometry) < 2 || geometry[0] != want.startPos || geometry[1] != want.length {
			t.Fatalf("skel %d geometry = %v, want [%d %d]", i, geometry, want.startPos, want.length)
		}
	}

	for _, entry := range chunkIndex.entries {
		seq := requiredTag(t, entry, 4, "sequence_number")[0]
		if seq < 0 || seq >= len(compiled.chunkTable) {
			t.Fatalf("chunk sequence %d outside compiled table", seq)
		}
		want := compiled.chunkTable[seq]
		insertPos := parseIndexLeadInt(t, entry)
		if insertPos != want.insertPos {
			t.Fatalf("chunk %d insertPos = %d, want %d", seq, insertPos, want.insertPos)
		}
		if fileNumber := requiredTag(t, entry, 3, "file_number")[0]; fileNumber != want.fileNumber {
			t.Fatalf("chunk %d file_number = %d, want %d", seq, fileNumber, want.fileNumber)
		}
		geometry := requiredTag(t, entry, 6, "chunk geometry")
		if len(geometry) < 2 || geometry[0] != want.startPos || geometry[1] != want.length {
			t.Fatalf("chunk %d geometry = %v, want [%d %d]", seq, geometry, want.startPos, want.length)
		}
	}

	second := findInspectedNCXEntry(t, ncxIndex, "第二章")
	posFID := requiredTag(t, second, 6, "pos_fid")
	if len(posFID) < 2 {
		t.Fatalf("second NCX pos_fid too short: %v", posFID)
	}
	offset := requiredTag(t, second, 1, "offset")[0]
	target := compiled.targets["text/chapter-002.xhtml#heading-002"]
	if target.aid == "" {
		t.Fatal("compiled book missing second heading target")
	}
	if posFID[0] != target.chunkSeq || posFID[1] != target.chunkOffset {
		t.Fatalf("second NCX pos_fid = %v, want [%d %d]", posFID, target.chunkSeq, target.chunkOffset)
	}
	if offset != target.absoluteOffset {
		t.Fatalf("second NCX offset = %d, want %d", offset, target.absoluteOffset)
	}

	chunkEntry := inspectedChunkBySequence(t, chunkIndex.entries, posFID[0])
	chunkInsertPos := parseIndexLeadInt(t, chunkEntry)
	chunkGeometry := requiredTag(t, chunkEntry, 6, "chunk geometry")
	if posFID[1] < 0 || posFID[1] >= chunkGeometry[1] {
		t.Fatalf("second NCX chunk offset = %d, chunk length = %d", posFID[1], chunkGeometry[1])
	}
	if chunkInsertPos+posFID[1] != offset {
		t.Fatalf("second NCX offset does not match chunk position: %d + %d != %d", chunkInsertPos, posFID[1], offset)
	}
	raw := inspectChunkRawBySequence(t, text, skelIndex.entries, chunkIndex.entries, posFID[0])
	if !bytes.Contains(raw, []byte(`aid="`+target.aid+`"`)) {
		t.Fatalf("second heading aid %q missing from target chunk", target.aid)
	}
	if !bytes.Contains(raw, []byte("第二章")) {
		t.Fatal("target chunk missing second chapter title")
	}

	guide := guideIndex.entries[0]
	guideTitleOffset := requiredTag(t, guide, 1, "guide title")[0]
	if title := guideIndex.cncxString(t, guideTitleOffset); title != "第一章" {
		t.Fatalf("guide title = %q, want 第一章", title)
	}
	guidePosFID := requiredTag(t, guide, 6, "guide pos_fid")
	firstTarget := compiled.targets["text/chapter-001.xhtml#heading-001"]
	if len(guidePosFID) < 2 || guidePosFID[0] != firstTarget.chunkSeq || guidePosFID[1] != firstTarget.chunkOffset {
		t.Fatalf("guide pos_fid = %v, want [%d %d]", guidePosFID, firstTarget.chunkSeq, firstTarget.chunkOffset)
	}
}

func TestBuildNCXTableUsesCalibreDepthOffsetOrder(t *testing.T) {
	toc := []ebook.TOCEntry{
		{
			Title: "Volume 1",
			Href:  "v1.xhtml",
			Children: []ebook.TOCEntry{
				{Title: "Chapter 1", Href: "c1.xhtml"},
				{Title: "Chapter 2", Href: "c2.xhtml"},
			},
		},
		{
			Title: "Volume 2",
			Href:  "v2.xhtml",
			Children: []ebook.TOCEntry{
				{Title: "Chapter 3", Href: "c3.xhtml"},
			},
		},
	}
	targets := map[string]target{
		"v1.xhtml": {absoluteOffset: 0},
		"c1.xhtml": {absoluteOffset: 100},
		"c2.xhtml": {absoluteOffset: 200},
		"v2.xhtml": {absoluteOffset: 300},
		"c3.xhtml": {absoluteOffset: 400},
	}

	got := buildNCXTable(toc, targets, 500)
	wantLabels := []string{"Volume 1", "Volume 2", "Chapter 1", "Chapter 2", "Chapter 3"}
	if len(got) != len(wantLabels) {
		t.Fatalf("NCX entries = %d, want %d", len(got), len(wantLabels))
	}
	for i, want := range wantLabels {
		if got[i].index != i || got[i].label != want {
			t.Fatalf("NCX entry %d = index %d %q, want index %d %q", i, got[i].index, got[i].label, i, want)
		}
	}
	if got[0].parent != -1 || got[0].firstChild != 2 || got[0].lastChild != 3 {
		t.Fatalf("Volume 1 relations = parent %d, children [%d,%d]", got[0].parent, got[0].firstChild, got[0].lastChild)
	}
	if got[1].parent != -1 || got[1].firstChild != 4 || got[1].lastChild != 4 {
		t.Fatalf("Volume 2 relations = parent %d, children [%d,%d]", got[1].parent, got[1].firstChild, got[1].lastChild)
	}
	if got[2].parent != 0 || got[3].parent != 0 || got[4].parent != 1 {
		t.Fatalf("chapter parents = [%d,%d,%d], want [0,0,1]", got[2].parent, got[3].parent, got[4].parent)
	}
}

func TestWriteGeneratedTextCoverToBinaryIndexes(t *testing.T) {
	source := txtbook.ToEBook(txtbook.Book{
		Title:  "测试书",
		Author: "作者",
		Cover:  true,
		Sections: []txtbook.Section{{
			ID:    "chapter-001",
			Title: "第一章",
			Blocks: []txtbook.Block{
				{Kind: txtbook.BlockHeading, Level: 2, Text: "第一章", ID: "heading-001"},
				{Kind: txtbook.BlockParagraph, Text: "正文。"},
			},
		}},
		Headings: []txtbook.Heading{{
			ID: "heading-001", Title: "第一章", Level: 2, SectionID: "chapter-001",
		}},
	})
	normalized := normalizeBook(source)
	compiled, err := compileBook(normalized)
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "cover.azw3")
	if err := Write(out, source, Options{}); err != nil {
		t.Fatal(err)
	}

	records := readAZW3Records(t, out)
	header := inspectAZW3MOBIHeader(t, records[0])
	text := decompressTextRecords(t, records[1:1+int(header.textRecordCount)])
	chunkIndex := inspectAZW3Index(t, records, header.chunkIndex)
	skelIndex := inspectAZW3Index(t, records, header.skelIndex)
	guideIndex := inspectAZW3Index(t, records, header.guideIndex)

	if len(skelIndex.entries) != 2 {
		t.Fatalf("skeleton entries = %d, want cover + content", len(skelIndex.entries))
	}
	var coverContent []byte
	for _, chunk := range chunksForFileNumber(t, chunkIndex.entries, 0) {
		seq := requiredTag(t, chunk, 4, "sequence_number")[0]
		coverContent = append(coverContent, inspectChunkRawBySequence(t, text, skelIndex.entries, chunkIndex.entries, seq)...)
	}
	for _, want := range []string{"测试书", "作者"} {
		if !bytes.Contains(coverContent, []byte(want)) {
			t.Fatalf("cover content missing %q: %q", want, coverContent)
		}
	}

	if len(guideIndex.entries) != 2 || guideIndex.entries[0].lead != "title-page" || guideIndex.entries[1].lead != "text" {
		t.Fatalf("guide entries = %#v", guideIndex.entries)
	}
	for i, href := range []string{"cover.xhtml", "text/chapter-001.xhtml"} {
		got := requiredTag(t, guideIndex.entries[i], 6, "guide pos_fid")
		want := compiled.targets[href]
		if len(got) < 2 || got[0] != want.chunkSeq || got[1] != want.chunkOffset {
			t.Fatalf("guide %q pos_fid = %v, want [%d %d]", guideIndex.entries[i].lead, got, want.chunkSeq, want.chunkOffset)
		}
	}
}

func navigationIndexTestBook() ebook.Book {
	return ebook.Book{
		Metadata: ebook.Metadata{Title: "导航测试", Author: "作者", Language: "zh-CN"},
		Spine: []ebook.Document{
			testDocument("text/chapter-001.xhtml", "第一章", "chapter-001", "heading-001", 4),
			testDocument("text/chapter-002.xhtml", "第二章", "chapter-002", "heading-002", 900),
			testDocument("text/chapter-003.xhtml", "第三章", "chapter-003", "heading-003", 5),
		},
		TOC: []ebook.TOCEntry{
			{Title: "第一章", Href: "text/chapter-001.xhtml#heading-001"},
			{Title: "第二章", Href: "text/chapter-002.xhtml#heading-002"},
			{Title: "第三章", Href: "text/chapter-003.xhtml#heading-003"},
		},
		Guide: []ebook.GuideRef{{
			Type:  "text",
			Title: "第一章",
			Href:  "text/chapter-001.xhtml#heading-001",
		}},
	}
}
