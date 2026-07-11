package epub

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeCSSReferences(t *testing.T) {
	got, imports, images, err := normalizeCSSReferences(`/*x*/@import "base.css"; p { background:url(../images/p.png); }`, "OPS/css/main.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "@import") || !strings.Contains(got, `url("OPS/images/p.png")`) {
		t.Fatalf("CSS = %q", got)
	}
	if !reflect.DeepEqual(imports, []string{"OPS/css/base.css"}) {
		t.Fatalf("imports = %#v", imports)
	}
	if !reflect.DeepEqual(images, []string{"OPS/images/p.png"}) {
		t.Fatalf("images = %#v", images)
	}
}

func TestExpandStylesheetsKeepsDocumentScope(t *testing.T) {
	deps := map[string][]string{"a.css": {"base.css"}, "b.css": nil}
	if got := expandStylesheets([]string{"a.css"}, deps); !reflect.DeepEqual(got, []string{"base.css", "a.css"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestReadLoadsImportedCSSAndReferencedImage(t *testing.T) {
	pngData, _ := encodedTestImages(t)
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("OPS/book.opf"),
		"OPS/book.opf": `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest>
<item id="c" href="text/c.xhtml" media-type="application/xhtml+xml"/><item id="main" href="css/main.css" media-type="text/css"/>
<item id="base" href="css/base.css" media-type="text/css"/><item id="dot" href="images/dot.png" media-type="image/png"/>
</manifest><spine><itemref idref="c"/></spine></package>`,
		"OPS/text/c.xhtml":   `<html xmlns="http://www.w3.org/1999/xhtml"><head><link rel="stylesheet" href="../css/main.css"/></head><body><p class="note">正文</p></body></html>`,
		"OPS/css/main.css":   `@import "base.css"; li { list-style-image:url(../images/dot.png); }`,
		"OPS/css/base.css":   `p { text-indent:2em; }`,
		"OPS/images/dot.png": string(pngData),
	})
	book, err := Read(filename, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(book.Spine[0].Stylesheets, []string{"OPS/css/base.css", "OPS/css/main.css"}) {
		t.Fatalf("stylesheets = %#v", book.Spine[0].Stylesheets)
	}
	if len(book.Resources) != 3 || book.Resources[2].Href != "OPS/images/dot.png" {
		t.Fatalf("resources = %#v", book.Resources)
	}
}

func TestReadRejectsCSSImportCycle(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/><item id="a" href="a.css" media-type="text/css"/><item id="b" href="b.css" media-type="text/css"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><head><link rel="stylesheet" href="a.css"/></head><body><p>x</p></body></html>`,
		"a.css":                  `@import "b.css";`, "b.css": `@import "a.css";`,
	})
	_, err := Read(filename, Options{})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v", err)
	}
}
