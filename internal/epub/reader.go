package epub

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type Options struct {
	DefaultLanguage string
}

func Read(filename string, opts Options) (ebook.Book, error) {
	a, err := openArchive(filename)
	if err != nil {
		return ebook.Book{}, err
	}
	defer a.Close()

	opfPath, err := readRootfile(a)
	if err != nil {
		return ebook.Book{}, err
	}
	pkg, err := readPackage(a, opfPath)
	if err != nil {
		return ebook.Book{}, err
	}

	items := make(map[string]manifestItem, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		items[item.ID] = item
	}

	book := ebook.Book{Metadata: ebook.Metadata{
		Title:      firstNonBlank(pkg.Metadata.Titles),
		Author:     strings.Join(nonBlank(pkg.Metadata.Creators), " & "),
		Language:   firstNonBlank(pkg.Metadata.Languages),
		Identifier: firstNonBlank(pkg.Metadata.Identifiers),
	}}
	if book.Metadata.Title == "" {
		book.Metadata.Title = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	if book.Metadata.Language == "" {
		book.Metadata.Language = strings.TrimSpace(opts.DefaultLanguage)
	}

	for i, ref := range pkg.Spine.ItemRefs {
		item, ok := items[ref.IDRef]
		if !ok {
			return ebook.Book{}, fmt.Errorf("spine item %q is missing from manifest", ref.IDRef)
		}
		if item.MediaType != "application/xhtml+xml" && item.MediaType != "text/html" {
			return ebook.Book{}, fmt.Errorf("unsupported spine media type %q for %q", item.MediaType, item.Href)
		}
		docPath, err := resolveReference(opfPath, item.Href)
		if err != nil {
			return ebook.Book{}, fmt.Errorf("resolve spine item %q: %w", item.Href, err)
		}
		data, err := a.read(referencePath(docPath))
		if err != nil {
			return ebook.Book{}, err
		}
		body, title, err := xhtmlBody(data, referencePath(docPath))
		if err != nil {
			return ebook.Book{}, err
		}
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		book.Spine = append(book.Spine, ebook.Document{Href: referencePath(docPath), Title: title, Body: body})
	}
	spinePaths := make(map[string]bool, len(book.Spine))
	for _, doc := range book.Spine {
		spinePaths[doc.Href] = true
	}

	book.TOC, err = readTOC(a, opfPath, pkg, items)
	if err != nil {
		return ebook.Book{}, err
	}
	if err := validateTOCTargets(book.TOC, spinePaths); err != nil {
		return ebook.Book{}, err
	}
	for _, ref := range pkg.Guide.References {
		href, err := resolveReference(opfPath, ref.Href)
		if err != nil {
			return ebook.Book{}, fmt.Errorf("resolve guide reference %q: %w", ref.Href, err)
		}
		if spinePaths[referencePath(href)] {
			book.Guide = append(book.Guide, ebook.GuideRef{Type: ref.Type, Title: ref.Title, Href: href})
		}
	}
	return book, nil
}

func validateTOCTargets(entries []ebook.TOCEntry, spinePaths map[string]bool) error {
	for _, entry := range entries {
		if !spinePaths[referencePath(entry.Href)] {
			return fmt.Errorf("TOC target %q is not present in the spine", entry.Href)
		}
		if err := validateTOCTargets(entry.Children, spinePaths); err != nil {
			return err
		}
	}
	return nil
}

func readTOC(a *archive, opfPath string, pkg packageDocument, items map[string]manifestItem) ([]ebook.TOCEntry, error) {
	for _, item := range pkg.Manifest.Items {
		if !hasProperty(item.Properties, "nav") {
			continue
		}
		navPath, err := resolveReference(opfPath, item.Href)
		if err != nil {
			return nil, err
		}
		data, err := a.read(referencePath(navPath))
		if err != nil {
			return nil, err
		}
		return readNav(data, referencePath(navPath))
	}

	ncx := items[pkg.Spine.TOC]
	if ncx.ID == "" {
		for _, item := range pkg.Manifest.Items {
			if item.MediaType == "application/x-dtbncx+xml" {
				ncx = item
				break
			}
		}
	}
	if ncx.ID == "" {
		return nil, nil
	}
	ncxPath, err := resolveReference(opfPath, ncx.Href)
	if err != nil {
		return nil, err
	}
	data, err := a.read(referencePath(ncxPath))
	if err != nil {
		return nil, err
	}
	return readNCX(data, referencePath(ncxPath))
}
