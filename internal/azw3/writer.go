package azw3

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type Options struct {
	SVGConverter SVGConverter
}

type SVGConverter interface {
	ToPNG(svg []byte) ([]byte, error)
}

func Write(path string, b ebook.Book, opts Options) error {
	normalized := normalizeBook(b)
	if err := prepareCoverAndSVG(&normalized, opts); err != nil {
		return err
	}
	normalized = normalizeBook(normalized)
	compiled, err := compileBook(normalized)
	if err != nil {
		return err
	}
	records, err := buildRecords(compiled)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return writePalmDB(file, compiled.metadata.Title, records)
}

func prepareCoverAndSVG(book *ebook.Book, opts Options) error {
	for i := range book.Resources {
		if book.Resources[i].MediaType != "image/svg+xml" {
			continue
		}
		if opts.SVGConverter == nil {
			return fmt.Errorf("SVG resource %q requires an SVG converter", book.Resources[i].Href)
		}
		pngData, err := opts.SVGConverter.ToPNG(book.Resources[i].Data)
		if err != nil {
			return fmt.Errorf("convert SVG resource %q: %w", book.Resources[i].Href, err)
		}
		if len(pngData) > maxConvertedSVGBytes {
			return fmt.Errorf("converted SVG resource %q exceeds size limit", book.Resources[i].Href)
		}
		_, format, err := image.DecodeConfig(bytes.NewReader(pngData))
		if err != nil || format != "png" {
			return fmt.Errorf("SVG converter returned invalid PNG for %q", book.Resources[i].Href)
		}
		book.Resources[i].MediaType = "image/png"
		book.Resources[i].Data = pngData
	}
	if book.Cover == nil || book.Cover.ImageHref == "" {
		return nil
	}
	pageHref := book.Cover.TitlePageHref
	found := false
	for _, doc := range book.Spine {
		if doc.Href == pageHref && pageHref != "" {
			found = true
			break
		}
	}
	if !found {
		pageHref = "kindle-go/cover.xhtml"
		book.Cover.TitlePageHref = pageHref
		body := ebook.Element("body", []ebook.Attr{ebook.A("class", "cover")}, ebook.Element("div", []ebook.Attr{ebook.A("class", "cover-image")}, ebook.Element("img", []ebook.Attr{ebook.A("src", book.Cover.ImageHref), ebook.A("alt", "Cover")})))
		book.Spine = append([]ebook.Document{{Href: pageHref, Title: "Cover", Body: body}}, book.Spine...)
	}
	for _, ref := range book.Guide {
		if ref.Type == "cover" {
			return nil
		}
	}
	book.Guide = append([]ebook.GuideRef{{Type: "cover", Title: "Cover", Href: pageHref}}, book.Guide...)
	return nil
}

const maxConvertedSVGBytes = 64 << 20
