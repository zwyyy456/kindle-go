package epub

import "testing"

func TestReadEPUB3CoverImage(t *testing.T) {
	pngData, _ := encodedTestImages(t)
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("OPS/book.opf"),
		"OPS/book.opf":           `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/><item id="cover" href="images/cover.png" media-type="image/png" properties="cover-image"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"OPS/c.xhtml":            `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`,
		"OPS/images/cover.png":   string(pngData),
	})
	book, err := Read(filename, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if book.Cover == nil || book.Cover.ImageHref != "OPS/images/cover.png" {
		t.Fatalf("cover = %#v", book.Cover)
	}
	if len(book.Resources) != 1 || book.Resources[0].Href != book.Cover.ImageHref {
		t.Fatalf("resources = %#v", book.Resources)
	}
}

func TestReadEPUB2CoverMeta(t *testing.T) {
	_, jpegData := encodedTestImages(t)
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata><meta name="cover" content="cover-image"/></metadata><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/><item id="cover-image" href="cover.jpg" media-type="image/jpeg"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`, "cover.jpg": string(jpegData),
	})
	book, err := Read(filename, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if book.Cover == nil || book.Cover.ImageHref != "cover.jpg" {
		t.Fatalf("cover = %#v", book.Cover)
	}
}

func TestReadGuideCoverPageFindsImage(t *testing.T) {
	pngData, _ := encodedTestImages(t)
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/><item id="cp" href="cover.xhtml" media-type="application/xhtml+xml"/><item id="i" href="cover.png" media-type="image/png"/></manifest><spine><itemref idref="c"/></spine><guide><reference type="cover" href="cover.xhtml"/></guide></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`, "cover.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body><img src="cover.png"/></body></html>`, "cover.png": string(pngData),
	})
	book, err := Read(filename, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if book.Cover == nil || book.Cover.ImageHref != "cover.png" || book.Cover.TitlePageHref != "cover.xhtml" {
		t.Fatalf("cover = %#v", book.Cover)
	}
}
