package azw3

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestCompileBookBuildsMultiDocumentSkeletonChunksAndTargets(t *testing.T) {
	b := normalizeBook(ebook.Book{
		Metadata: ebook.Metadata{Title: "测试书", Language: "zh-CN"},
		Spine: []ebook.Document{
			testDocument("text/chapter-001.xhtml", "第一章", "chapter-001", "heading-001", 3),
			testDocument("text/chapter-002.xhtml", "第二章", "chapter-002", "heading-002", 900),
		},
		TOC: []ebook.TOCEntry{
			{Title: "第一章", Href: "text/chapter-001.xhtml#heading-001"},
			{Title: "第二章", Href: "text/chapter-002.xhtml#heading-002"},
		},
	})

	compiled, err := compileBook(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.documents) != 2 {
		t.Fatalf("documents = %d", len(compiled.documents))
	}
	if len(compiled.skelTable) != len(compiled.documents) {
		t.Fatalf("skel/doc count = %d/%d", len(compiled.skelTable), len(compiled.documents))
	}
	totalChunks := 0
	for i, doc := range compiled.documents {
		totalChunks += len(doc.chunks)
		if compiled.skelTable[i].chunkCount != len(doc.chunks) {
			t.Fatalf("doc %d skel chunk count = %d, chunks = %d", i, compiled.skelTable[i].chunkCount, len(doc.chunks))
		}
		reconstructed := reconstructDocument(doc)
		if bytes.Count(reconstructed, []byte("<html ")) != 1 || bytes.Count(reconstructed, []byte("</html>")) != 1 {
			t.Fatalf("doc %d is not one reconstructed html document", i)
		}
		if !bytes.Contains(reconstructed, []byte(doc.title)) {
			t.Fatalf("doc %d reconstructed content missing title", i)
		}
	}
	if len(compiled.chunkTable) != totalChunks {
		t.Fatalf("chunk table count = %d, chunks = %d", len(compiled.chunkTable), totalChunks)
	}

	target := compiled.targets["text/chapter-002.xhtml#heading-002"]
	if target.aid == "" {
		t.Fatal("missing target for second heading")
	}
	if target.chunkSeq < 0 || target.chunkSeq >= len(compiled.chunkTable) {
		t.Fatalf("target chunk seq = %d, chunk table count = %d", target.chunkSeq, len(compiled.chunkTable))
	}
	chunk := compiled.chunkTable[target.chunkSeq]
	if target.chunkOffset < 0 || target.chunkOffset >= chunk.length {
		t.Fatalf("target chunk offset = %d, chunk length = %d", target.chunkOffset, chunk.length)
	}
	raw := contentChunkBySeq(t, compiled, target.chunkSeq).raw
	if !bytes.Contains(raw, []byte(`aid="`+target.aid+`"`)) {
		t.Fatalf("target aid %q not present in target chunk", target.aid)
	}
}

func testDocument(href, title, sectionID, headingID string, paragraphs int) ebook.Document {
	children := []*ebook.Node{
		ebook.Element("h2", []ebook.Attr{ebook.A("id", headingID)}, ebook.Text(title)),
	}
	for i := 0; i < paragraphs; i++ {
		children = append(children, ebook.Element("p", nil, ebook.Text(fmt.Sprintf("第 %d 段中文正文，用来覆盖较长章节的 chunk 拆分。", i+1))))
	}
	return ebook.Document{
		Href:  href,
		Title: title,
		Body: ebook.Element("body", nil,
			ebook.Element("section", []ebook.Attr{ebook.A("id", sectionID)}, children...),
		),
	}
}

func reconstructDocument(doc compiledDocument) []byte {
	out := append([]byte(nil), doc.skeleton...)
	for _, chunk := range doc.chunks {
		pos := chunk.insertPos - doc.flowStart
		out = append(out[:pos], append(chunk.raw, out[pos:]...)...)
	}
	return out
}

func contentChunkBySeq(t *testing.T, compiled compiledBook, seq int) contentChunk {
	t.Helper()
	for _, doc := range compiled.documents {
		for _, chunk := range doc.chunks {
			if chunk.seq == seq {
				return chunk
			}
		}
	}
	t.Fatalf("missing content chunk seq %d", seq)
	return contentChunk{}
}
