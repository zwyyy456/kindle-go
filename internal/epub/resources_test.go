package epub

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func TestReadLoadsReferencedJPEGAndPNGInFirstReferenceOrder(t *testing.T) {
	pngData, jpegData := encodedTestImages(t)
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("OEBPS/book.opf"),
		"OEBPS/book.opf":         `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest><item id="c1" href="text/ch.xhtml" media-type="application/xhtml+xml"/><item id="png" href="images/p.png" media-type="image/png"/><item id="jpg" href="images/j.jpg" media-type="image/jpeg"/></manifest><spine><itemref idref="c1"/></spine></package>`,
		"OEBPS/text/ch.xhtml":    `<html xmlns="http://www.w3.org/1999/xhtml"><body><p><img src="../images/p.png" alt="P" width="12"/><img src="../images/j.jpg" alt="J"/></p></body></html>`,
		"OEBPS/images/p.png":     string(pngData),
		"OEBPS/images/j.jpg":     string(jpegData),
	})
	book, err := Read(filename, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Resources) != 2 || book.Resources[0].Href != "OEBPS/images/p.png" || book.Resources[1].Href != "OEBPS/images/j.jpg" {
		t.Fatalf("resources = %#v", book.Resources)
	}
	img := findElement(book.Spine[0].Body, "img")
	if img == nil || ebook.AttrValue(img, "src") != "OEBPS/images/p.png" || ebook.AttrValue(img, "alt") != "P" || ebook.AttrValue(img, "width") != "12" {
		t.Fatalf("img = %#v", img)
	}
}

func TestReadRejectsImageMediaTypeMismatch(t *testing.T) {
	pngData, _ := encodedTestImages(t)
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/><item id="bad" href="bad.jpg" media-type="image/jpeg"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><body><img src="bad.jpg"/></body></html>`,
		"bad.jpg":                string(pngData),
	})
	_, err := Read(filename, Options{})
	if err == nil || !strings.Contains(err.Error(), "declares") {
		t.Fatalf("error = %v", err)
	}
}

func encodedTestImages(t *testing.T) ([]byte, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 30), B: 100, A: 255})
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
