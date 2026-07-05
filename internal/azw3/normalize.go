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
	}
	if len(b.TOC) == 0 {
		for _, doc := range b.Spine {
			b.TOC = append(b.TOC, ebook.TOCEntry{Title: doc.Title, Href: doc.Href})
		}
	}
	return b
}
