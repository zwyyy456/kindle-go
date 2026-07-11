package azw3

import (
	"strings"
	"testing"
)

func TestSanitizeCSSKeepsBasicLayoutAndRewritesImage(t *testing.T) {
	catalog := resourceCatalog{resources: []compiledResource{{mediaType: "image/png"}}, byHref: map[string]int{"OPS/p.png": 0}}
	got, err := sanitizeCSS(`p.note { text-indent:2em; color:red; background-image:url("OPS/p.png"); } img { width:50%; position:fixed; }`, catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"text-indent:2em", "width:50%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("CSS %q missing %q", got, want)
		}
	}
	for _, unwanted := range []string{"color", "background", "position"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("CSS %q contains %q", got, unwanted)
		}
	}
}

func TestSanitizeCSSRewritesAllowedURL(t *testing.T) {
	catalog := resourceCatalog{resources: []compiledResource{{mediaType: "image/png"}}, byHref: map[string]int{"OPS/p.png": 0}}
	got, err := sanitizeCSS(`li { list-style-image:url("OPS/p.png"); }`, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "kindle:embed:0001?mime=image/png") {
		t.Fatalf("CSS = %q", got)
	}
}
