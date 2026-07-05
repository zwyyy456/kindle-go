package azw3

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func buildTOCRecord(b ebook.Book) []byte {
	var w bytes.Buffer
	w.WriteString("INDX\n")
	w.WriteString(`<nav epub:type="toc"><ol>`)
	for _, entry := range b.TOC {
		writeTOCEntry(&w, entry)
	}
	w.WriteString(`</ol></nav>`)
	return w.Bytes()
}

func writeTOCEntry(w *bytes.Buffer, entry ebook.TOCEntry) {
	title := strings.TrimSpace(entry.Title)
	if title == "" {
		title = entry.Href
	}
	fmt.Fprintf(w, `<li><a href="%s">%s</a>`, html.EscapeString(entry.Href), html.EscapeString(title))
	if len(entry.Children) > 0 {
		w.WriteString("<ol>")
		for _, child := range entry.Children {
			writeTOCEntry(w, child)
		}
		w.WriteString("</ol>")
	}
	w.WriteString("</li>")
}
