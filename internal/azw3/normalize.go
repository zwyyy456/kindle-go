package azw3

import (
	"fmt"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func normalizeBook(b ebook.Book) ebook.Book {
	if strings.TrimSpace(b.Metadata.Title) == "" {
		b.Metadata.Title = "Untitled"
	}
	if strings.TrimSpace(b.Metadata.Language) == "" {
		b.Metadata.Language = "zh-CN"
	}
	if strings.TrimSpace(b.Metadata.Identifier) == "" {
		b.Metadata.Identifier = "kindle-go:" + b.Metadata.Title
	}
	if b.Style.LineHeight <= 0 {
		b.Style.LineHeight = 1.7
	}
	if strings.TrimSpace(b.Style.ParagraphIndent) == "" {
		b.Style.ParagraphIndent = "2em"
	}
	if strings.TrimSpace(b.Style.ParagraphSpacing) == "" {
		b.Style.ParagraphSpacing = "0"
	}
	if strings.TrimSpace(b.Style.TextAlign) == "" {
		b.Style.TextAlign = "justify"
	}
	if len(b.Spine) == 0 {
		b.Spine = []ebook.Document{{
			Href:  "text/chapter-001.xhtml",
			Title: "正文",
			Body:  ebook.Element("body", nil),
		}}
	}
	for i := range b.Spine {
		if strings.TrimSpace(b.Spine[i].Href) == "" {
			b.Spine[i].Href = fmt.Sprintf("text/chapter-%03d.xhtml", i+1)
		}
		if strings.TrimSpace(b.Spine[i].Title) == "" {
			b.Spine[i].Title = fmt.Sprintf("Chapter %d", i+1)
		}
		if b.Spine[i].Body == nil {
			b.Spine[i].Body = ebook.Element("body", nil)
		}
		ensureBodyIDs(b.Spine[i].Body, i+1)
	}
	if len(b.TOC) == 0 {
		for _, doc := range b.Spine {
			b.TOC = append(b.TOC, ebook.TOCEntry{Title: doc.Title, Href: doc.Href})
		}
	}
	if len(b.Guide) == 0 && len(b.Spine) > 0 {
		b.Guide = []ebook.GuideRef{{
			Type:  "text",
			Title: b.Spine[0].Title,
			Href:  b.Spine[0].Href,
		}}
	}
	return b
}

func ensureBodyIDs(n *ebook.Node, docSeq int) {
	if n == nil || n.Type != ebook.ElementNode {
		return
	}
	if n.Data == "body" && ebook.AttrValue(n, "id") == "" {
		n.Attr = append(n.Attr, ebook.A("id", fmt.Sprintf("body-%03d", docSeq)))
	}
	headingSeq := 0
	var walk func(*ebook.Node)
	walk = func(node *ebook.Node) {
		if node == nil || node.Type != ebook.ElementNode {
			return
		}
		switch node.Data {
		case "section":
			if ebook.AttrValue(node, "id") == "" {
				node.Attr = append(node.Attr, ebook.A("id", fmt.Sprintf("chapter-%03d", docSeq)))
			}
		case "h1", "h2", "h3", "h4", "h5", "h6":
			headingSeq++
			if ebook.AttrValue(node, "id") == "" {
				node.Attr = append(node.Attr, ebook.A("id", fmt.Sprintf("heading-%03d-%03d", docSeq, headingSeq)))
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(n)
}
