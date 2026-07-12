package epub

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

// Write serializes the shared ebook model as a reflowable EPUB 3 book with an
// EPUB 2 NCX fallback. Input-specific parsing belongs before this boundary.
func Write(filename string, book ebook.Book) error {
	book = normalizeWritableBook(book)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	if err := writeMimetype(zw); err != nil {
		return err
	}
	id := uuid()
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	files := map[string][]byte{
		"META-INF/container.xml": []byte(containerDocumentXML()),
		"OEBPS/styles.css":       []byte(bookCSS(book.Style)),
		"OEBPS/nav.xhtml":        []byte(navXHTML(book)),
		"OEBPS/toc.ncx":          []byte(tocNCX(book, id)),
		"OEBPS/content.opf":      []byte(opf(book, id, modified)),
	}
	for _, doc := range book.Spine {
		files["OEBPS/"+doc.Href] = []byte(documentXHTML(book, doc))
	}
	for _, resource := range book.Resources {
		if resource.Href != "" {
			files["OEBPS/"+resource.Href] = resource.Data
		}
	}
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return zw.Close()
}

func normalizeWritableBook(book ebook.Book) ebook.Book {
	if strings.TrimSpace(book.Metadata.Title) == "" {
		book.Metadata.Title = "Untitled"
	}
	if strings.TrimSpace(book.Metadata.Language) == "" {
		book.Metadata.Language = "zh-CN"
	}
	if len(book.Spine) == 0 {
		book.Spine = []ebook.Document{{Href: "text/chapter-001.xhtml", Title: "正文", Body: ebook.Element("body", nil)}}
	}
	if len(book.TOC) == 0 {
		for _, doc := range book.Spine {
			book.TOC = append(book.TOC, ebook.TOCEntry{Title: doc.Title, Href: doc.Href})
		}
	}
	return book
}

func writeMimetype(zw *zip.Writer) error {
	header := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	header.SetMode(0o644)
	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "application/epub+zip")
	return err
}

func containerDocumentXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
`
}

func opf(book ebook.Book, id, modified string) string {
	var manifest, spine strings.Builder
	manifest.WriteString(`    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n")
	manifest.WriteString(`    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n")
	manifest.WriteString(`    <item id="style" href="styles.css" media-type="text/css"/>` + "\n")
	for i, doc := range book.Spine {
		fmt.Fprintf(&manifest, `    <item id="doc-%d" href="%s" media-type="application/xhtml+xml"/>`+"\n", i+1, xmlEsc(doc.Href))
		fmt.Fprintf(&spine, `    <itemref idref="doc-%d"/>`+"\n", i+1)
	}
	for i, resource := range book.Resources {
		fmt.Fprintf(&manifest, `    <item id="resource-%d" href="%s" media-type="%s"/>`+"\n", i+1, xmlEsc(resource.Href), xmlEsc(resource.MediaType))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/">
    <dc:identifier id="bookid">urn:uuid:%s</dc:identifier>
    <dc:title>%s</dc:title><dc:language>%s</dc:language><dc:creator>%s</dc:creator>
    <meta property="dcterms:modified">%s</meta>
  </metadata>
  <manifest>
%s  </manifest>
  <spine toc="ncx">
%s  </spine>
</package>
`, xmlEsc(id), xmlEsc(book.Metadata.Title), xmlEsc(book.Metadata.Language), xmlEsc(book.Metadata.Author), xmlEsc(modified), manifest.String(), spine.String())
}

func documentXHTML(book ebook.Book, doc ebook.Document) string {
	var body strings.Builder
	writeNode(&body, doc.Body, 1)
	styleHref := relativeHref(doc.Href, "styles.css")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="%s" lang="%s">
<head><title>%s</title><link rel="stylesheet" type="text/css" href="%s"/></head>
%s
</html>
`, xmlEsc(book.Metadata.Language), xmlEsc(book.Metadata.Language), xmlEsc(doc.Title), xmlEsc(styleHref), body.String())
}

func writeNode(out *strings.Builder, node *ebook.Node, depth int) {
	if node == nil {
		return
	}
	if node.Type == ebook.TextNode {
		out.WriteString(xmlEsc(node.Data))
		return
	}
	name := strings.ToLower(node.Data)
	if name == "" {
		return
	}
	indent := strings.Repeat("  ", depth)
	out.WriteString(indent + "<" + name)
	for _, attr := range node.Attr {
		fmt.Fprintf(out, ` %s="%s"`, attr.Key, xmlEsc(attr.Val))
	}
	if (name == "br" || name == "img") && len(node.Children) == 0 {
		out.WriteString("/>")
		return
	}
	out.WriteString(">")
	block := hasElementChildren(node)
	if block && len(node.Children) > 0 {
		out.WriteByte('\n')
	}
	for _, child := range node.Children {
		writeNode(out, child, depth+1)
	}
	if block && len(node.Children) > 0 {
		out.WriteByte('\n')
		out.WriteString(indent)
	}
	out.WriteString("</" + name + ">")
}

func hasElementChildren(node *ebook.Node) bool {
	for _, child := range node.Children {
		if child != nil && child.Type == ebook.ElementNode {
			return true
		}
	}
	return false
}

func navXHTML(book ebook.Book) string {
	var list strings.Builder
	writeNavList(&list, book.TOC, 3)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="%s">
<head><title>目录</title><link rel="stylesheet" href="styles.css"/></head>
<body><nav epub:type="toc"><h1>目录</h1><ol>
%s    </ol></nav></body></html>
`, xmlEsc(book.Metadata.Language), list.String())
}

func writeNavList(out *strings.Builder, entries []ebook.TOCEntry, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, entry := range entries {
		fmt.Fprintf(out, `%s<li><a href="%s">%s</a>`, indent, xmlEsc(entry.Href), xmlEsc(entry.Title))
		if len(entry.Children) > 0 {
			out.WriteString("\n" + indent + "  <ol>\n")
			writeNavList(out, entry.Children, depth+2)
			out.WriteString(indent + "  </ol>\n" + indent)
		}
		out.WriteString("</li>\n")
	}
}

func tocNCX(book ebook.Book, id string) string {
	var navMap strings.Builder
	playOrder := 1
	writeNCXEntries(&navMap, book.TOC, &playOrder, 2)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head><meta name="dtb:uid" content="urn:uuid:%s"/></head>
  <docTitle><text>%s</text></docTitle><navMap>
%s  </navMap>
</ncx>
`, xmlEsc(id), xmlEsc(book.Metadata.Title), navMap.String())
}

func writeNCXEntries(out *strings.Builder, entries []ebook.TOCEntry, playOrder *int, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, entry := range entries {
		current := *playOrder
		(*playOrder)++
		fmt.Fprintf(out, `%s<navPoint id="navpoint-%d" playOrder="%d">`+"\n", indent, current, current)
		fmt.Fprintf(out, "%s  <navLabel><text>%s</text></navLabel>\n", indent, xmlEsc(entry.Title))
		fmt.Fprintf(out, `%s  <content src="%s"/>`+"\n", indent, xmlEsc(entry.Href))
		writeNCXEntries(out, entry.Children, playOrder, depth+1)
		out.WriteString(indent + "</navPoint>\n")
	}
}

func bookCSS(style ebook.Style) string {
	return fmt.Sprintf(`body { line-height: %.2f; text-align: %s; }
p { margin: %s 0; text-indent: %s; }
h1, h2, h3, h4, h5, h6 { text-indent: 0; line-height: 1.35; page-break-before: always; }
.cover { text-align: center; padding-top: 25%%; }
.cover h1 { font-size: 1.6em; page-break-before: auto; }
.cover .author { margin-top: 2em; text-indent: 0; }
`, positiveFloat(style.LineHeight, 1.7), safeCSSIdent(style.TextAlign, "justify"), safeCSSLength(style.ParagraphSpacing, "0"), safeCSSLength(style.ParagraphIndent, "2em"))
}

func relativeHref(from, to string) string {
	fromDir := path.Dir(from)
	rel, err := filepath.Rel(filepath.FromSlash(fromDir), filepath.FromSlash(to))
	if err != nil {
		return to
	}
	return filepath.ToSlash(rel)
}

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16]))
}

func xmlEsc(s string) string { return html.EscapeString(s) }

func positiveFloat(value, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func safeCSSIdent(value, fallback string) string {
	value = strings.TrimSpace(value)
	for _, r := range value {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return fallback
		}
	}
	if value == "" {
		return fallback
	}
	return value
}

func safeCSSLength(value, fallback string) string {
	value = strings.TrimSpace(value)
	for _, r := range value {
		if !(r == '.' || r == '%' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return fallback
		}
	}
	if value == "" {
		return fallback
	}
	return value
}
