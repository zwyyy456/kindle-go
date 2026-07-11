package azw3

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type fakeSVGConverter struct{ png []byte }

func (f fakeSVGConverter) ToPNG([]byte) ([]byte, error) { return f.png, nil }

func TestCoverCreatesTitlePageAndEXTHOffset(t *testing.T) {
	book := coverTestBook(t, "image/png", testPNG(t))
	unused := append(testPNG(t), 0)
	book.Resources = append([]ebook.Resource{{Href: "unused.png", MediaType: "image/png", Data: unused}}, book.Resources...)
	normalized := normalizeBook(book)
	if err := prepareCoverAndSVG(&normalized, Options{}); err != nil {
		t.Fatal(err)
	}
	normalized = normalizeBook(normalized)
	if normalized.Spine[0].Href != "kindle-go/cover.xhtml" || normalized.Guide[0].Type != "cover" {
		t.Fatalf("spine/guide = %#v / %#v", normalized.Spine, normalized.Guide)
	}
	compiled, err := compileBook(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.coverResourceOffset != 1 {
		t.Fatalf("cover offset = %d", compiled.coverResourceOffset)
	}
	exth := buildEXTH(compiled.metadata, compiled.coverResourceOffset)
	if got, ok := exthUint32(exth, 201); !ok || got != 1 {
		t.Fatalf("EXTH 201 = %d, %v", got, ok)
	}
}

func TestSVGRequiresConverterAndAcceptsPNG(t *testing.T) {
	book := coverTestBook(t, "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	if err := prepareCoverAndSVG(&book, Options{}); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("error = %v", err)
	}
	book = coverTestBook(t, "image/svg+xml", []byte(`<svg/>`))
	if err := prepareCoverAndSVG(&book, Options{SVGConverter: fakeSVGConverter{png: testPNG(t)}}); err != nil {
		t.Fatal(err)
	}
	if book.Resources[0].MediaType != "image/png" {
		t.Fatalf("resource = %#v", book.Resources[0])
	}
}

func TestCalibreExtractsNativeCover(t *testing.T) {
	tool, err := exec.LookPath("ebook-meta")
	if err != nil {
		t.Skip("Calibre ebook-meta is not installed")
	}
	dir := t.TempDir()
	bookPath := filepath.Join(dir, "book.azw3")
	coverPath := filepath.Join(dir, "cover.png")
	if err := Write(bookPath, coverTestBook(t, "image/png", testPNG(t)), Options{}); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(tool, bookPath, "--get-cover", coverPath).CombinedOutput()
	if err != nil {
		t.Fatalf("ebook-meta: %v\n%s", err, output)
	}
	data, err := os.ReadFile(coverPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || (format != "png" && format != "jpeg") {
		t.Fatalf("extracted cover format = %q, error = %v", format, err)
	}
}

func coverTestBook(t *testing.T, media string, data []byte) ebook.Book {
	return ebook.Book{Metadata: ebook.Metadata{Title: "Book"}, Spine: []ebook.Document{{Href: "text.xhtml", Title: "Text", Body: ebook.Element("body", nil, ebook.Element("p", nil, ebook.Text("text")))}}, Resources: []ebook.Resource{{Href: "cover", MediaType: media, Data: data}}, Cover: &ebook.Cover{ImageHref: "cover"}}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.Black)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func exthUint32(exth []byte, wanted uint32) (uint32, bool) {
	count := binary.BigEndian.Uint32(exth[8:12])
	off := 12
	for i := uint32(0); i < count; i++ {
		typ := binary.BigEndian.Uint32(exth[off : off+4])
		size := int(binary.BigEndian.Uint32(exth[off+4 : off+8]))
		if typ == wanted && size == 12 {
			return binary.BigEndian.Uint32(exth[off+8 : off+12]), true
		}
		off += size
	}
	return 0, false
}
