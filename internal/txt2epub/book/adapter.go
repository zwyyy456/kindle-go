package book

import (
	"fmt"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func ToEBook(b Book) ebook.Book {
	docs := make([]ebook.Document, 0, len(b.Sections)+1)
	if b.Cover {
		docs = append(docs, textCoverDocument(b))
	}
	for _, section := range b.Sections {
		href := sectionHref(section)
		docs = append(docs, ebook.Document{
			Href:  href,
			Title: section.Title,
			Body:  sectionBody(section),
		})
	}
	out := ebook.Book{
		Metadata: ebook.Metadata{
			Title:      b.Title,
			Author:     b.Author,
			Language:   b.Language,
			Identifier: "kindle-go:" + b.Title,
		},
		Style: ebook.Style{
			LineHeight:       b.Style.LineHeight,
			ParagraphIndent:  b.Style.ParagraphIndent,
			ParagraphSpacing: b.Style.ParagraphSpacing,
			TextAlign:        b.Style.TextAlign,
		},
		Spine: docs,
		TOC:   tocEntries(b),
	}
	if b.Cover {
		out.Guide = []ebook.GuideRef{{
			Type:  "title-page",
			Title: "扉页",
			Href:  "cover.xhtml",
		}}
		if len(b.Sections) > 0 {
			out.Guide = append(out.Guide, ebook.GuideRef{
				Type:  "text",
				Title: b.Sections[0].Title,
				Href:  sectionHref(b.Sections[0]),
			})
		}
	}
	return out
}

func textCoverDocument(b Book) ebook.Document {
	return ebook.Document{
		Href:  "cover.xhtml",
		Title: b.Title,
		Body: ebook.Element("body", nil,
			ebook.Element("section", []ebook.Attr{
				ebook.A("class", "cover"),
			},
				ebook.Element("h1", nil, ebook.Text(b.Title)),
				ebook.Element("p", []ebook.Attr{ebook.A("class", "author")}, ebook.Text(b.Author)),
			),
		),
	}
}

func sectionBody(section Section) *ebook.Node {
	children := make([]*ebook.Node, 0, len(section.Blocks))
	for _, block := range section.Blocks {
		switch block.Kind {
		case BlockHeading:
			tag := "h1"
			if block.Level >= 2 {
				tag = "h2"
			}
			attr := []ebook.Attr(nil)
			if block.ID != "" {
				attr = append(attr, ebook.A("id", block.ID))
			}
			children = append(children, ebook.Element(tag, attr, ebook.Text(block.Text)))
		case BlockParagraph:
			children = append(children, ebook.Element("p", nil, ebook.Text(block.Text)))
		}
	}
	sectionAttrs := []ebook.Attr(nil)
	if section.ID != "" {
		sectionAttrs = append(sectionAttrs, ebook.A("id", section.ID))
	}
	return ebook.Element("body", nil, ebook.Element("section", sectionAttrs, children...))
}

func tocEntries(b Book) []ebook.TOCEntry {
	if len(b.Headings) == 0 {
		if len(b.Sections) == 0 {
			return nil
		}
		first := b.Sections[0]
		title := first.Title
		if title == "" {
			title = "正文"
		}
		return []ebook.TOCEntry{{Title: title, Href: sectionHref(first)}}
	}
	if onlyLevel2Headings(b.Headings) {
		entries := make([]ebook.TOCEntry, 0, len(b.Headings))
		for _, h := range b.Headings {
			entries = append(entries, headingTOCEntry(h))
		}
		return entries
	}

	var entries []ebook.TOCEntry
	for i := 0; i < len(b.Headings); i++ {
		h := b.Headings[i]
		if h.Level != 1 {
			continue
		}
		entry := headingTOCEntry(h)
		for j := i + 1; j < len(b.Headings); j++ {
			child := b.Headings[j]
			if child.Level == 1 {
				break
			}
			if child.Level == 2 {
				entry.Children = append(entry.Children, headingTOCEntry(child))
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func headingTOCEntry(h Heading) ebook.TOCEntry {
	href := fmt.Sprintf("text/%s.xhtml", h.SectionID)
	if h.ID != "" {
		href += "#" + h.ID
	}
	return ebook.TOCEntry{Title: h.Title, Href: href}
}

func sectionHref(section Section) string {
	if section.ID == "" {
		return "text/section.xhtml"
	}
	return fmt.Sprintf("text/%s.xhtml", section.ID)
}

func onlyLevel2Headings(headings []Heading) bool {
	for _, h := range headings {
		if h.Level == 1 {
			return false
		}
	}
	return len(headings) > 0
}
