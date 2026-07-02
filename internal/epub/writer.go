package epub

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zwyyy/txt2epub/internal/book"
)

func Write(path string, b book.Book) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
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
	files := map[string]string{
		"META-INF/container.xml": containerXML(),
		"OEBPS/styles.css":       css(b),
		"OEBPS/nav.xhtml":        navXHTML(b),
		"OEBPS/toc.ncx":          tocNCX(b, id),
		"OEBPS/content.opf":      opf(b, id, modified),
	}
	if b.Cover {
		files["OEBPS/cover.xhtml"] = coverXHTML(b)
	}
	for _, section := range b.Sections {
		files["OEBPS/text/"+section.ID+".xhtml"] = sectionXHTML(b, section)
	}

	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, content); err != nil {
			return err
		}
	}
	return zw.Close()
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

func containerXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`
}

func opf(b book.Book, id, modified string) string {
	var manifest strings.Builder
	if b.Cover {
		manifest.WriteString(`    <item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>` + "\n")
	}
	manifest.WriteString(`    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n")
	manifest.WriteString(`    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n")
	manifest.WriteString(`    <item id="style" href="styles.css" media-type="text/css"/>` + "\n")
	for _, section := range b.Sections {
		fmt.Fprintf(&manifest, `    <item id="%s" href="text/%s.xhtml" media-type="application/xhtml+xml"/>`+"\n", section.ID, section.ID)
	}

	var spine strings.Builder
	if b.Cover {
		spine.WriteString(`    <itemref idref="cover" linear="yes"/>` + "\n")
	}
	for _, section := range b.Sections {
		fmt.Fprintf(&spine, `    <itemref idref="%s"/>`+"\n", section.ID)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/">
    <dc:identifier id="bookid">urn:uuid:%s</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:language>%s</dc:language>
    <dc:creator>%s</dc:creator>
    <meta property="dcterms:modified">%s</meta>
  </metadata>
  <manifest>
%s  </manifest>
  <spine toc="ncx">
%s  </spine>
</package>
`, xmlEsc(id), xmlEsc(b.Title), xmlEsc(b.Language), xmlEsc(b.Author), xmlEsc(modified), manifest.String(), spine.String())
}

func coverXHTML(b book.Book) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="%s" lang="%s">
<head>
  <title>%s</title>
  <link rel="stylesheet" type="text/css" href="styles.css"/>
</head>
<body>
  <section class="cover">
    <h1>%s</h1>
    <p class="author">%s</p>
  </section>
</body>
</html>
`, xmlEsc(b.Language), xmlEsc(b.Language), xmlEsc(b.Title), xmlEsc(b.Title), xmlEsc(b.Author))
}

func sectionXHTML(b book.Book, section book.Section) string {
	var body strings.Builder
	for _, block := range section.Blocks {
		switch block.Kind {
		case book.BlockHeading:
			tag := "h1"
			if block.Level == 2 {
				tag = "h2"
			}
			fmt.Fprintf(&body, `    <%s id="%s">%s</%s>`+"\n", tag, xmlEsc(block.ID), xmlEsc(block.Text), tag)
		case book.BlockParagraph:
			fmt.Fprintf(&body, "    <p>%s</p>\n", xmlEsc(block.Text))
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="%s" lang="%s">
<head>
  <title>%s</title>
  <link rel="stylesheet" type="text/css" href="../styles.css"/>
</head>
<body>
  <section>
%s  </section>
</body>
</html>
`, xmlEsc(b.Language), xmlEsc(b.Language), xmlEsc(section.Title), body.String())
}

func navXHTML(b book.Book) string {
	var list strings.Builder
	if len(b.Headings) == 0 {
		list.WriteString(`      <li><a href="text/chapter-001.xhtml">正文</a></li>` + "\n")
	} else {
		for i := 0; i < len(b.Headings); i++ {
			h := b.Headings[i]
			if h.Level == 2 {
				continue
			}
			fmt.Fprintf(&list, `      <li><a href="text/%s.xhtml#%s">%s</a>`, h.SectionID, h.ID, xmlEsc(h.Title))
			children := childHeadings(b.Headings, i)
			if len(children) > 0 {
				list.WriteString("\n        <ol>\n")
				for _, child := range children {
					fmt.Fprintf(&list, `          <li><a href="text/%s.xhtml#%s">%s</a></li>`+"\n", child.SectionID, child.ID, xmlEsc(child.Title))
				}
				list.WriteString("        </ol>\n      </li>\n")
			} else {
				list.WriteString("</li>\n")
			}
		}
		if onlyH2(b.Headings) {
			for _, h := range b.Headings {
				fmt.Fprintf(&list, `      <li><a href="text/%s.xhtml#%s">%s</a></li>`+"\n", h.SectionID, h.ID, xmlEsc(h.Title))
			}
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="%s" lang="%s">
<head>
  <title>目录</title>
  <link rel="stylesheet" type="text/css" href="styles.css"/>
</head>
<body>
  <nav epub:type="toc" xmlns:epub="http://www.idpf.org/2007/ops">
    <h1>目录</h1>
    <ol>
%s    </ol>
  </nav>
</body>
</html>
`, xmlEsc(b.Language), xmlEsc(b.Language), list.String())
}

func tocNCX(b book.Book, id string) string {
	var navMap strings.Builder
	playOrder := 1
	if len(b.Headings) == 0 {
		navPoint(&navMap, "navpoint-1", playOrder, "正文", "text/chapter-001.xhtml", "")
	} else {
		for i := 0; i < len(b.Headings); i++ {
			h := b.Headings[i]
			if h.Level == 2 {
				continue
			}
			children := childHeadings(b.Headings, i)
			if len(children) == 0 {
				navPoint(&navMap, h.ID, playOrder, h.Title, "text/"+h.SectionID+".xhtml", h.ID)
				playOrder++
				continue
			}
			fmt.Fprintf(&navMap, `    <navPoint id="%s" playOrder="%d">`+"\n", xmlEsc(h.ID), playOrder)
			fmt.Fprintf(&navMap, "      <navLabel><text>%s</text></navLabel>\n", xmlEsc(h.Title))
			fmt.Fprintf(&navMap, `      <content src="text/%s.xhtml#%s"/>`+"\n", h.SectionID, h.ID)
			playOrder++
			for _, child := range children {
				navPoint(&navMap, child.ID, playOrder, child.Title, "text/"+child.SectionID+".xhtml", child.ID)
				playOrder++
			}
			navMap.WriteString("    </navPoint>\n")
		}
		if onlyH2(b.Headings) {
			for _, h := range b.Headings {
				navPoint(&navMap, h.ID, playOrder, h.Title, "text/"+h.SectionID+".xhtml", h.ID)
				playOrder++
			}
		}
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="urn:uuid:%s"/>
    <meta name="dtb:depth" content="2"/>
    <meta name="dtb:totalPageCount" content="0"/>
    <meta name="dtb:maxPageNumber" content="0"/>
  </head>
  <docTitle><text>%s</text></docTitle>
  <navMap>
%s  </navMap>
</ncx>
`, xmlEsc(id), xmlEsc(b.Title), navMap.String())
}

func navPoint(w *strings.Builder, id string, playOrder int, title, src, fragment string) {
	if fragment != "" {
		src += "#" + fragment
	}
	fmt.Fprintf(w, `    <navPoint id="%s" playOrder="%d">`+"\n", xmlEsc(id), playOrder)
	fmt.Fprintf(w, "      <navLabel><text>%s</text></navLabel>\n", xmlEsc(title))
	fmt.Fprintf(w, `      <content src="%s"/>`+"\n", xmlEsc(src))
	w.WriteString("    </navPoint>\n")
}

func css(b book.Book) string {
	return fmt.Sprintf(`body {
  line-height: %.2f;
  text-align: %s;
}
p {
  margin: %s 0;
  text-indent: %s;
}
h1, h2 {
  text-indent: 0;
  line-height: 1.35;
  page-break-before: always;
}
.cover {
  text-align: center;
  padding-top: 25%%;
}
.cover h1 {
  font-size: 1.6em;
  page-break-before: auto;
}
.cover .author {
  margin-top: 2em;
  text-indent: 0;
}
`, b.Style.LineHeight, safeCSSIdent(b.Style.TextAlign, "justify"), b.Style.ParagraphSpacing, safeCSSLength(b.Style.ParagraphIndent, "2em"))
}

func childHeadings(headings []book.Heading, h1Index int) []book.Heading {
	var children []book.Heading
	for i := h1Index + 1; i < len(headings); i++ {
		if headings[i].Level == 1 {
			break
		}
		if headings[i].Level == 2 {
			children = append(children, headings[i])
		}
	}
	return children
}

func onlyH2(headings []book.Heading) bool {
	for _, h := range headings {
		if h.Level == 1 {
			return false
		}
	}
	return len(headings) > 0
}

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func xmlEsc(s string) string {
	return html.EscapeString(s)
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
