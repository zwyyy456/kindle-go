package azw3

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func compileBook(b ebook.Book) (compiledBook, error) {
	resources, err := compileResources(b.Resources)
	if err != nil {
		return compiledBook{}, err
	}
	prepared := make([]preparedDocument, 0, len(b.Spine))
	for i, doc := range b.Spine {
		aidByID, bodyAID := assignAIDs(doc.Body, i)
		prepared = append(prepared, preparedDocument{document: doc, aidByID: aidByID, bodyAID: bodyAID})
	}
	if err := rewriteImageReferences(prepared, resources); err != nil {
		return compiledBook{}, err
	}
	documentCSS, err := compileDocumentCSS(b, resources)
	if err != nil {
		return compiledBook{}, err
	}
	links, err := prepareInternalLinks(prepared)
	if err != nil {
		return compiledBook{}, err
	}
	provisional, err := compilePreparedBook(b, prepared, resources.resources, documentCSS)
	if err != nil {
		return compiledBook{}, err
	}
	if err := resolveInternalLinks(links, provisional.targets); err != nil {
		return compiledBook{}, err
	}
	compiled, err := compilePreparedBook(b, prepared, resources.resources, documentCSS)
	if err != nil {
		return compiledBook{}, err
	}
	compiled.coverResourceOffset = nullIndex
	if b.Cover != nil && b.Cover.ImageHref != "" {
		index, ok := resources.byHref[b.Cover.ImageHref]
		if !ok {
			return compiledBook{}, fmt.Errorf("cover image %q has no resource", b.Cover.ImageHref)
		}
		compiled.coverResourceOffset = uint32(index)
	}
	return compiled, nil
}

type preparedDocument struct {
	document ebook.Document
	aidByID  map[string]string
	bodyAID  string
}

func compilePreparedBook(b ebook.Book, prepared []preparedDocument, resources []compiledResource, documentCSS map[string]string) (compiledBook, error) {
	var flow bytes.Buffer
	targets := map[string]target{}
	docs := make([]compiledDocument, 0, len(b.Spine))
	var chunkTable []chunkEntry
	chunkSeq := 0

	for i, source := range prepared {
		doc := source.document
		rendered := []byte(renderDocument(b.Metadata, b.Style, doc, documentCSS[doc.Href]))
		skeleton, rawChunks, insertOffset := splitSkeletonChunks(rendered)
		flowStart := flow.Len()
		compiled := compiledDocument{
			href:       doc.Href,
			title:      doc.Title,
			skeleton:   skeleton,
			aidByID:    source.aidByID,
			bodyAID:    source.bodyAID,
			flowStart:  flowStart,
			rebuildLen: len(rendered),
		}
		flow.Write(skeleton)
		chunkStart := 0
		for _, raw := range rawChunks {
			content := contentChunk{
				seq:       chunkSeq,
				raw:       raw,
				insertPos: flowStart + insertOffset + chunkStart,
				startPos:  chunkStart,
				length:    len(raw),
				selector:  "S-" + source.bodyAID,
			}
			compiled.chunks = append(compiled.chunks, content)
			chunkTable = append(chunkTable, chunkEntry{
				insertPos:      content.insertPos,
				selector:       content.selector,
				fileNumber:     i,
				sequenceNumber: content.seq,
				startPos:       content.startPos,
				length:         content.length,
			})
			flow.Write(raw)
			chunkStart += len(raw)
			chunkSeq++
		}
		docs = append(docs, compiled)
		resolveDocumentTargets(rendered, compiled, targets)
	}
	mainText := flow.Bytes()
	flows := [][]byte{
		mainText,
		[]byte(css(b.Style)),
		[]byte("@page {\n    margin-bottom: 5pt;\n    margin-top: 5pt\n    }"),
		[]byte("\nli {\n    list-style-type: none\n    }\na {\n    text-decoration: none\n    }\n"),
	}
	var allFlows bytes.Buffer
	flowBounds := make([][2]int, 0, len(flows))
	for _, contents := range flows {
		start := allFlows.Len()
		allFlows.Write(contents)
		flowBounds = append(flowBounds, [2]int{start, allFlows.Len()})
	}
	text := allFlows.Bytes()
	return compiledBook{
		metadata:   b.Metadata,
		style:      b.Style,
		text:       text,
		records:    chunkBytes(text, textRecordSize),
		flowBounds: flowBounds,
		documents:  docs,
		targets:    targets,
		skelTable:  buildSkelTable(docs),
		chunkTable: chunkTable,
		tocTable:   buildNCXTable(b.TOC, targets, len(mainText)),
		guideTable: buildGuideTable(b.Guide, targets),
		resources:  resources,
	}, nil
}

func renderDocument(meta ebook.Metadata, style ebook.Style, doc ebook.Document, extraCSS string) string {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	body.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:mbp="https://kindlegen.s3.amazonaws.com/AmazonKindlePublishingGuidelines.pdf"`)
	if meta.Language != "" {
		fmt.Fprintf(&body, ` xml:lang="%s" lang="%s"`, html.EscapeString(meta.Language), html.EscapeString(meta.Language))
	}
	body.WriteString(">\n<head>\n")
	fmt.Fprintf(&body, "<title>%s</title>\n", html.EscapeString(doc.Title))
	body.WriteString("<link rel=\"stylesheet\" type=\"text/css\" href=\"kindle:flow:0001?mime=text/css\"/>\n")
	body.WriteString("<link rel=\"stylesheet\" type=\"text/css\" href=\"kindle:flow:0002?mime=text/css\"/>\n")
	if extraCSS != "" {
		body.WriteString("<style type=\"text/css\">\n")
		body.WriteString(extraCSS)
		body.WriteString("</style>\n")
	}
	body.WriteString("</head>\n")
	renderNode(&body, doc.Body)
	body.WriteString("\n</html>\n")
	return body.String()
}

func assignAIDs(root *ebook.Node, docIndex int) (map[string]string, string) {
	aidByID := map[string]string{}
	seq := 0
	bodyAID := ""
	var walk func(*ebook.Node)
	walk = func(n *ebook.Node) {
		if n == nil || n.Type != ebook.ElementNode {
			return
		}
		if aidableElement(n.Data) || ebook.AttrValue(n, "id") != "" {
			aid := base32(docIndex*1_000_000 + seq)
			seq++
			setAttr(n, "aid", aid)
			if n.Data == "body" {
				bodyAID = aid
				aidByID[""] = aid
			}
			if id := ebook.AttrValue(n, "id"); id != "" {
				aidByID[id] = aid
			}
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(root)
	return aidByID, bodyAID
}

func aidableElement(name string) bool {
	switch name {
	case "body", "section", "article", "div", "h1", "h2", "h3", "h4", "h5", "h6", "p", "a", "span", "li", "ol", "ul", "blockquote":
		return true
	default:
		return false
	}
}

func setAttr(n *ebook.Node, key, value string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, ebook.A(key, value))
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

func findIDOffsets(data []byte) map[string]int {
	out := map[string]int{}
	pos := 0
	for {
		idx := bytes.Index(data[pos:], []byte(` id="`))
		if idx < 0 {
			return out
		}
		attrStart := pos + idx + len(` id="`)
		attrEnd := bytes.IndexByte(data[attrStart:], '"')
		if attrEnd < 0 {
			return out
		}
		id := html.UnescapeString(string(data[attrStart : attrStart+attrEnd]))
		tagStart := bytes.LastIndexByte(data[:pos+idx], '<')
		if tagStart < 0 {
			tagStart = pos + idx
		}
		if id != "" {
			out[id] = tagStart
		}
		pos = attrStart + attrEnd + 1
	}
}

func resolveDocumentTargets(rendered []byte, doc compiledDocument, targets map[string]target) {
	for id, aid := range doc.aidByID {
		offset := findAIDOffset(rendered, aid)
		if offset < 0 {
			continue
		}
		t := locateTarget(doc, aid, doc.flowStart+offset)
		href := doc.href
		if id != "" {
			href += "#" + id
		}
		targets[href] = t
	}
	if body, ok := targets[doc.href+"#"]; ok {
		targets[doc.href] = body
	} else if aid := doc.bodyAID; aid != "" {
		offset := findAIDOffset(rendered, aid)
		if offset >= 0 {
			targets[doc.href] = locateTarget(doc, aid, doc.flowStart+offset)
		}
	}
}

func findAIDOffset(data []byte, aid string) int {
	pattern := []byte(` aid="` + aid + `"`)
	idx := bytes.Index(data, pattern)
	if idx < 0 {
		return -1
	}
	tagStart := bytes.LastIndexByte(data[:idx], '<')
	if tagStart < 0 {
		return idx
	}
	return tagStart
}

func locateTarget(doc compiledDocument, aid string, absolute int) target {
	for _, chunk := range doc.chunks {
		if absolute >= chunk.insertPos && absolute < chunk.insertPos+chunk.length {
			return target{
				aid:            aid,
				absoluteOffset: absolute,
				chunkSeq:       chunk.seq,
				chunkOffset:    absolute - chunk.insertPos,
			}
		}
	}
	for _, chunk := range doc.chunks {
		if absolute < chunk.insertPos {
			return target{aid: aid, absoluteOffset: absolute, chunkSeq: chunk.seq}
		}
	}
	if len(doc.chunks) == 0 {
		return target{aid: aid, absoluteOffset: absolute}
	}
	last := doc.chunks[len(doc.chunks)-1]
	return target{
		aid:            aid,
		absoluteOffset: absolute,
		chunkSeq:       last.seq,
		chunkOffset:    max(0, min(absolute-last.insertPos, last.length-1)),
	}
}

func base32(num int) string {
	const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUV"
	if num == 0 {
		return "0"
	}
	var out []byte
	for num > 0 {
		out = append(out, digits[num%32])
		num /= 32
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
