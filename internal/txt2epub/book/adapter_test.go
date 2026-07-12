package book

import (
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestToEBookBuildsSpineAndBodyTree(t *testing.T) {
	b := Book{
		Title:    "测试书",
		Author:   "作者",
		Language: "zh-CN",
		Style: config.Style{
			LineHeight:       1.8,
			ParagraphIndent:  "2em",
			ParagraphSpacing: "0.4em",
			TextAlign:        "justify",
		},
		Sections: []Section{{
			ID:    "chapter-001",
			Title: "第一章 开始",
			Blocks: []Block{
				{Kind: BlockHeading, Level: 2, Text: "第一章 开始", ID: "heading-001"},
				{Kind: BlockParagraph, Text: "正文。"},
			},
		}},
		Headings: []Heading{{ID: "heading-001", Title: "第一章 开始", Level: 2, SectionID: "chapter-001"}},
	}

	got := ToEBook(b)
	if got.Metadata.Title != "测试书" || got.Metadata.Author != "作者" || got.Metadata.Language != "zh-CN" {
		t.Fatalf("metadata = %#v", got.Metadata)
	}
	if got.Metadata.Identifier != "kindle-go:测试书" {
		t.Fatalf("identifier = %q", got.Metadata.Identifier)
	}
	if got.Style.LineHeight != 1.8 || got.Style.ParagraphSpacing != "0.4em" {
		t.Fatalf("style = %#v", got.Style)
	}
	if len(got.Spine) != 1 {
		t.Fatalf("spine len = %d", len(got.Spine))
	}
	doc := got.Spine[0]
	if doc.Href != "text/chapter-001.xhtml" {
		t.Fatalf("doc href = %q", doc.Href)
	}
	section := doc.Body.Children[0]
	if ebook.AttrValue(section, "id") != "chapter-001" {
		t.Fatalf("section id = %q", ebook.AttrValue(section, "id"))
	}
	h := section.Children[0]
	if h.Data != "h2" || ebook.AttrValue(h, "id") != "heading-001" || h.Children[0].Data != "第一章 开始" {
		t.Fatalf("heading node = %#v", h)
	}
	p := section.Children[1]
	if p.Data != "p" || p.Children[0].Data != "正文。" {
		t.Fatalf("paragraph node = %#v", p)
	}
}

func TestToEBookBuildsNestedTOC(t *testing.T) {
	b := Book{Headings: []Heading{
		{ID: "h1", Title: "第一卷", Level: 1, SectionID: "chapter-001"},
		{ID: "h2", Title: "第一章", Level: 2, SectionID: "chapter-002"},
		{ID: "h3", Title: "第二章", Level: 2, SectionID: "chapter-003"},
		{ID: "h4", Title: "第二卷", Level: 1, SectionID: "chapter-004"},
	}}
	got := ToEBook(b)
	if len(got.TOC) != 2 {
		t.Fatalf("toc len = %d", len(got.TOC))
	}
	if got.TOC[0].Href != "text/chapter-001.xhtml#h1" || len(got.TOC[0].Children) != 2 {
		t.Fatalf("first toc = %#v", got.TOC[0])
	}
	if got.TOC[0].Children[1].Href != "text/chapter-003.xhtml#h3" {
		t.Fatalf("second child = %#v", got.TOC[0].Children[1])
	}
}

func TestToEBookPrependsGeneratedTextCover(t *testing.T) {
	b := Book{
		Title:  "测试书",
		Author: "作者",
		Cover:  true,
		Sections: []Section{{
			ID:    "chapter-001",
			Title: "第一章",
		}},
	}

	got := ToEBook(b)
	if len(got.Spine) != 2 {
		t.Fatalf("spine len = %d", len(got.Spine))
	}
	cover := got.Spine[0]
	if cover.Href != "cover.xhtml" || cover.Title != "测试书" {
		t.Fatalf("cover document = %#v", cover)
	}
	section := cover.Body.Children[0]
	if ebook.AttrValue(section, "class") != "cover" {
		t.Fatalf("cover section = %#v", section)
	}
	var text string
	for _, child := range section.Children {
		for _, node := range child.Children {
			text += node.Data
		}
	}
	if text != "测试书作者" {
		t.Fatalf("cover text = %q", text)
	}
	if got.Spine[1].Href != "text/chapter-001.xhtml" {
		t.Fatalf("content document = %#v", got.Spine[1])
	}
	if len(got.Guide) != 2 || got.Guide[0].Type != "title-page" || got.Guide[1].Type != "text" {
		t.Fatalf("guide = %#v", got.Guide)
	}
}
