package azw3

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	epubreader "github.com/flashdict/kindle2flashdict/internal/epub"
)

func TestEPUBReaderToAZW3NavigationTargetsRealChunks(t *testing.T) {
	input := writeEPUBIntegrationFixture(t)
	book, err := epubreader.Read(input, epubreader.Options{DefaultLanguage: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	normalized := normalizeBook(book)
	compiled, err := compileBook(normalized)
	if err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(t.TempDir(), "book.azw3")
	if err := Write(output, book, Options{}); err != nil {
		t.Fatal(err)
	}
	records := readAZW3Records(t, output)
	header := inspectAZW3MOBIHeader(t, records[0])
	text := decompressTextRecords(t, records[1:int(header.firstNonText)])
	skelIndex := inspectAZW3Index(t, records, header.skelIndex)
	chunkIndex := inspectAZW3Index(t, records, header.chunkIndex)
	ncxIndex := inspectAZW3Index(t, records, header.ncxIndex)

	if len(skelIndex.entries) != 2 {
		t.Fatalf("skeleton entries = %d", len(skelIndex.entries))
	}
	second := findInspectedNCXEntry(t, ncxIndex, "第二章")
	posFID := requiredTag(t, second, 6, "pos_fid")
	target := compiled.targets["OEBPS/Text/ch2.xhtml#two"]
	if len(posFID) < 2 || posFID[0] != target.chunkSeq || posFID[1] != target.chunkOffset {
		t.Fatalf("second NCX pos_fid = %v, want [%d %d]", posFID, target.chunkSeq, target.chunkOffset)
	}
	raw := inspectChunkRawBySequence(t, text, skelIndex.entries, chunkIndex.entries, posFID[0])
	if !bytes.Contains(raw, []byte("第二章")) || !bytes.Contains(raw, []byte(`aid="`+target.aid+`"`)) {
		t.Fatalf("target chunk does not contain second chapter target: %q", raw)
	}
	for _, want := range []string{"EPUB 集成测试", "测试作者", "zh-CN"} {
		if !bytes.Contains(records[0], []byte(want)) {
			t.Fatalf("header missing metadata %q", want)
		}
	}
}

func writeEPUBIntegrationFixture(t *testing.T) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "book.epub")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	files := []struct{ name, data string }{
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>EPUB 集成测试</dc:title><dc:creator>测试作者</dc:creator><dc:language>zh-CN</dc:language><dc:identifier>epub-test</dc:identifier></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c1" href="Text/ch1.xhtml" media-type="application/xhtml+xml"/><item id="c2" href="Text/ch2.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`},
		{"OEBPS/nav.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="Text/ch1.xhtml#one">第一章</a></li><li><a href="Text/ch2.xhtml#two">第二章</a></li></ol></nav></body></html>`},
		{"OEBPS/Text/ch1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><section><h1 id="one">第一章</h1><p>正文一。</p></section></body></html>`},
		{"OEBPS/Text/ch2.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><section><h1 id="two">第二章</h1><p>正文二。</p></section></body></html>`},
	}
	for _, entry := range files {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}
