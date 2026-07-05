package azw3

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func compileBook(b ebook.Book) (compiledBook, error) {
	var body bytes.Buffer
	for i, doc := range b.Spine {
		if i > 0 {
			body.WriteString("\n<mbp:pagebreak/>\n")
		}
		body.WriteString(renderDocument(b.Metadata, doc))
	}
	text := body.Bytes()
	return compiledBook{
		metadata: b.Metadata,
		text:     text,
		chunks:   chunkBytes(text, textRecordSize),
		toc:      buildTOCRecord(b),
	}, nil
}

func renderDocument(meta ebook.Metadata, doc ebook.Document) string {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	body.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:mbp="https://kindlegen.s3.amazonaws.com/AmazonKindlePublishingGuidelines.pdf"`)
	if meta.Language != "" {
		fmt.Fprintf(&body, ` xml:lang="%s" lang="%s"`, html.EscapeString(meta.Language), html.EscapeString(meta.Language))
	}
	body.WriteString(">\n<head>\n")
	fmt.Fprintf(&body, "<title>%s</title>\n", html.EscapeString(doc.Title))
	body.WriteString("</head>\n")
	renderNode(&body, doc.Body)
	body.WriteString("\n</html>\n")
	return body.String()
}

func renderNode(w *bytes.Buffer, n *ebook.Node) {
	if n == nil {
		return
	}
	if n.Type == ebook.TextNode {
		w.WriteString(html.EscapeString(n.Data))
		return
	}
	name := safeElementName(n.Data)
	if name == "" {
		name = "span"
	}
	w.WriteByte('<')
	w.WriteString(name)
	for _, attr := range n.Attr {
		key := safeAttrName(attr.Key)
		if key == "" {
			continue
		}
		fmt.Fprintf(w, ` %s="%s"`, key, html.EscapeString(attr.Val))
	}
	w.WriteByte('>')
	for _, child := range n.Children {
		renderNode(w, child)
	}
	fmt.Fprintf(w, "</%s>", name)
}

func safeElementName(name string) string {
	name = strings.TrimSpace(name)
	for _, r := range name {
		if !(r == '-' || r == ':' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return name
}

func safeAttrName(name string) string {
	return safeElementName(name)
}
