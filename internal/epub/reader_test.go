package epub

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestReadEPUB3MetadataSpineNavGuideAndPaths(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("OEBPS/content.opf"),
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier>book-id</dc:identifier><dc:title>测试 EPUB</dc:title>
    <dc:creator>作者甲</dc:creator><dc:creator>作者乙</dc:creator><dc:language>zh-CN</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav/nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="Text/ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="Text/sub/ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2" linear="no"/></spine>
  <guide><reference type="text" title="正文" href="Text/./ch1.xhtml#one"/></guide>
</package>`,
		"OEBPS/nav/nav.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body>
<nav epub:type="toc"><ol>
  <li><a href="../Text/./ch1.xhtml#one"><span>第一章</span></a>
    <ol><li><a href="../Text/sub/../sub/ch2.xhtml#two">第二章</a></li></ol>
  </li>
</ol></nav></body></html>`,
		"OEBPS/Text/ch1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>第一章</title></head><body>
<main><article><h1 id="one">第一章</h1><p>中文正文&nbsp;一。</p>
<a href="sub/ch2.xhtml#two">下一章</a><script>不应保留</script></article></main>
</body></html>`,
		"OEBPS/Text/sub/ch2.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><section><h2 id="two">第二章</h2><p>正文二。</p></section></body></html>`,
	})

	book, err := Read(filename, Options{DefaultLanguage: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if book.Metadata.Title != "测试 EPUB" || book.Metadata.Author != "作者甲 & 作者乙" || book.Metadata.Language != "zh-CN" || book.Metadata.Identifier != "book-id" {
		t.Fatalf("metadata = %#v", book.Metadata)
	}
	if len(book.Spine) != 2 {
		t.Fatalf("spine len = %d", len(book.Spine))
	}
	if book.Spine[0].Href != "OEBPS/Text/ch1.xhtml" || book.Spine[1].Href != "OEBPS/Text/sub/ch2.xhtml" {
		t.Fatalf("spine hrefs = %#v", book.Spine)
	}
	if book.Spine[1].Title != "第二章" {
		t.Fatalf("heading-derived title = %q", book.Spine[1].Title)
	}
	firstText := textContent(book.Spine[0].Body)
	if !strings.Contains(firstText, "中文正文") || strings.Contains(firstText, "不应保留") {
		t.Fatalf("sanitized first document text = %q", firstText)
	}
	if findElement(book.Spine[0].Body, "main") != nil || findElement(book.Spine[0].Body, "article") != nil {
		t.Fatal("unknown structural wrappers were not unwrapped")
	}
	link := findElement(book.Spine[0].Body, "a")
	if ebook.AttrValue(link, "href") != "OEBPS/Text/sub/ch2.xhtml#two" {
		t.Fatalf("normalized body href = %q", ebook.AttrValue(link, "href"))
	}
	if len(book.TOC) != 1 || book.TOC[0].Href != "OEBPS/Text/ch1.xhtml#one" || len(book.TOC[0].Children) != 1 || book.TOC[0].Children[0].Href != "OEBPS/Text/sub/ch2.xhtml#two" {
		t.Fatalf("toc = %#v", book.TOC)
	}
	if len(book.Guide) != 1 || book.Guide[0].Href != "OEBPS/Text/ch1.xhtml#one" {
		t.Fatalf("guide = %#v", book.Guide)
	}
	if book.Resources != nil {
		t.Fatalf("resources = %#v, want nil in text-only phase", book.Resources)
	}
}

func TestReadEPUB2FallsBackToNCXAndDefaultLanguage(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("OPS/book.opf"),
		"OPS/book.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>EPUB 2</dc:title></metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="c1" href="text/c1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx"><itemref idref="c1"/></spine>
</package>`,
		"OPS/toc.ncx": `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
  <navPoint><navLabel><text>第一章</text></navLabel><content src="text/./c1.xhtml#start"/>
    <navPoint><navLabel><text>小节</text></navLabel><content src="text/c1.xhtml#sub"/></navPoint>
  </navPoint>
</navMap></ncx>`,
		"OPS/text/c1.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1 id="start">第一章</h1><p id="sub">内容</p></body></html>`,
	})

	book, err := Read(filename, Options{DefaultLanguage: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if book.Metadata.Language != "zh-CN" {
		t.Fatalf("language = %q", book.Metadata.Language)
	}
	if len(book.TOC) != 1 || book.TOC[0].Href != "OPS/text/c1.xhtml#start" || len(book.TOC[0].Children) != 1 || book.TOC[0].Children[0].Href != "OPS/text/c1.xhtml#sub" {
		t.Fatalf("toc = %#v", book.TOC)
	}
}

func TestReadRejectsRootfileOutsideArchive(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("../outside.opf"),
	})
	if _, err := Read(filename, Options{}); err == nil || !strings.Contains(err.Error(), "escapes archive root") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadSkipsGuideTargetOutsideSpine(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf": `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<metadata/><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/><item id="extra" href="extra.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="c1"/></spine><guide><reference type="cover" href="extra.xhtml"/></guide></package>`,
		"nav.xhtml":   `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="c1.xhtml">正文</a></li></ol></nav></body></html>`,
		"c1.xhtml":    `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`,
		"extra.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>额外页面</p></body></html>`,
	})
	book, err := Read(filename, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Guide) != 0 {
		t.Fatalf("guide = %#v", book.Guide)
	}
}

func TestReadRejectsTOCTargetOutsideSpine(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`,
		"nav.xhtml":              `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="missing.xhtml">错误目标</a></li></ol></nav></body></html>`,
		"c1.xhtml":               `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`,
	})
	if _, err := Read(filename, Options{}); err == nil || !strings.Contains(err.Error(), "not present in the spine") {
		t.Fatalf("error = %v", err)
	}
}

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "fixture.epub")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	if mimetype, ok := files["mimetype"]; ok {
		header := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(mimetype)); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for name := range files {
		if name != "mimetype" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
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

func containerFixture(rootfile string) string {
	return `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="` + rootfile + `" media-type="application/oebps-package+xml"/></rootfiles>
</container>`
}
