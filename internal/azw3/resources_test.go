package azw3

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestWriteImageRecordsAndDeduplicatesAliases(t *testing.T) {
	pngData, jpegData := azw3TestImages(t)
	book := ebook.Book{
		Metadata: ebook.Metadata{Title: "图片", Language: "zh-CN"},
		Spine: []ebook.Document{{
			Href: "text/ch.xhtml", Title: "正文",
			Body: ebook.Element("body", nil,
				ebook.Element("p", nil,
					ebook.Element("img", []ebook.Attr{ebook.A("src", "images/a.jpg"), ebook.A("alt", "A")}),
					ebook.Element("img", []ebook.Attr{ebook.A("src", "images/a-copy.jpg"), ebook.A("alt", "A2")}),
					ebook.Element("img", []ebook.Attr{ebook.A("src", "images/b.png"), ebook.A("alt", "B")}),
				),
			),
		}},
		Resources: []ebook.Resource{
			{Href: "images/a.jpg", MediaType: "image/jpeg", Data: jpegData},
			{Href: "images/a-copy.jpg", MediaType: "image/jpeg", Data: append([]byte(nil), jpegData...)},
			{Href: "images/b.png", MediaType: "image/png", Data: pngData},
		},
	}
	compiled, err := compileBook(normalizeBook(book))
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.resources) != 2 {
		t.Fatalf("compiled resources = %d", len(compiled.resources))
	}
	if bytes.Count(compiled.text, []byte("kindle:embed:0001?mime=image/jpeg")) != 2 || !bytes.Contains(compiled.text, []byte("kindle:embed:0002?mime=image/png")) {
		t.Fatalf("compiled image references = %q", compiled.text)
	}

	out := filepath.Join(t.TempDir(), "images.azw3")
	if err := Write(out, book, Options{}); err != nil {
		t.Fatal(err)
	}
	records := readAZW3Records(t, out)
	firstImage := binary.BigEndian.Uint32(records[0][108:112])
	if firstImage == nullIndex || int(firstImage)+1 >= len(records) {
		t.Fatalf("first image record = %d, records = %d", firstImage, len(records))
	}
	if !bytes.HasPrefix(records[firstImage], []byte{0xff, 0xd8, 0xff}) || !bytes.HasPrefix(records[firstImage+1], []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("resource records have wrong formats")
	}
	for i := 0; i < 2; i++ {
		if _, _, err := image.DecodeConfig(bytes.NewReader(records[int(firstImage)+i])); err != nil {
			t.Fatalf("decode image record %d: %v", i, err)
		}
	}
}

func TestCompileBookRejectsMissingImageResource(t *testing.T) {
	book := normalizeBook(ebook.Book{Spine: []ebook.Document{{
		Href: "text/ch.xhtml", Title: "正文",
		Body: ebook.Element("body", nil, ebook.Element("img", []ebook.Attr{ebook.A("src", "missing.png")})),
	}}})
	if _, err := compileBook(book); err == nil {
		t.Fatal("expected missing image resource error")
	}
}

func azw3TestImages(t *testing.T) ([]byte, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 30), B: 90, A: 255})
		}
	}
	var pngOut, jpegOut bytes.Buffer
	if err := png.Encode(&pngOut, img); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegOut, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return pngOut.Bytes(), jpegOut.Bytes()
}
