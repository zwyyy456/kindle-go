package azw3

import (
	"bytes"
	"strings"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestCompileBookRewritesCrossDocumentFootnoteLinks(t *testing.T) {
	forward := ebook.Element("a", []ebook.Attr{
		ebook.A("href", "OEBPS/Text/notes.xhtml#note-1"),
		ebook.A("epub:type", "noteref"),
	}, ebook.Text("[1]"))
	back := ebook.Element("a", []ebook.Attr{
		ebook.A("href", "OEBPS/Text/chapter.xhtml#back-1"),
	}, ebook.Text("返回"))
	book := normalizeBook(ebook.Book{
		Metadata: ebook.Metadata{Title: "脚注", Language: "zh-CN"},
		Spine: []ebook.Document{
			{
				Href: "OEBPS/Text/chapter.xhtml", Title: "正文",
				Body: ebook.Element("body", nil, ebook.Element("p", []ebook.Attr{ebook.A("id", "back-1")}, ebook.Text("正文"), forward)),
			},
			{
				Href: "OEBPS/Text/notes.xhtml", Title: "脚注",
				Body: ebook.Element("body", nil, ebook.Element("aside", []ebook.Attr{ebook.A("id", "note-1"), ebook.A("epub:type", "footnote")}, ebook.Text("注释"), back)),
			},
		},
	})

	compiled, err := compileBook(book)
	if err != nil {
		t.Fatal(err)
	}
	forwardTarget := compiled.targets["OEBPS/Text/notes.xhtml#note-1"]
	backTarget := compiled.targets["OEBPS/Text/chapter.xhtml#back-1"]
	wantForward := "kindle:pos:fid:" + paddedBase32(forwardTarget.chunkSeq, 4) + ":off:" + paddedBase32(forwardTarget.chunkOffset, 10)
	wantBack := "kindle:pos:fid:" + paddedBase32(backTarget.chunkSeq, 4) + ":off:" + paddedBase32(backTarget.chunkOffset, 10)
	text := compiled.text
	for _, want := range []string{wantForward, wantBack, `epub:type="noteref"`, `epub:type="footnote"`} {
		if !bytes.Contains(text, []byte(want)) {
			t.Fatalf("compiled text missing %q", want)
		}
	}
	if bytes.Contains(text, []byte(".xhtml#")) {
		t.Fatalf("compiled text retained unresolved internal link: %s", text)
	}
}

func TestCompileBookRejectsMissingInternalLinkTarget(t *testing.T) {
	book := normalizeBook(ebook.Book{
		Spine: []ebook.Document{{
			Href: "text/chapter.xhtml", Title: "正文",
			Body: ebook.Element("body", nil, ebook.Element("a", []ebook.Attr{ebook.A("href", "#missing")}, ebook.Text("坏链接"))),
		}},
	})
	_, err := compileBook(book)
	if err == nil || !strings.Contains(err.Error(), `text/chapter.xhtml#missing`) {
		t.Fatalf("error = %v", err)
	}
}

func TestCompileBookKeepsExternalLink(t *testing.T) {
	book := normalizeBook(ebook.Book{
		Spine: []ebook.Document{{
			Href: "text/chapter.xhtml", Title: "正文",
			Body: ebook.Element("body", nil, ebook.Element("a", []ebook.Attr{ebook.A("href", "https://example.com")}, ebook.Text("外链"))),
		}},
	})
	compiled, err := compileBook(book)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(compiled.text, []byte(`href="https://example.com"`)) {
		t.Fatal("external link was rewritten")
	}
}
