package epub

import (
	"errors"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type Options struct {
	DefaultLanguage string
}

func Read(filename string, opts Options) (ebook.Book, error) {
	analysis := Analyze(filename, opts)
	if len(analysis.Issues) != 0 {
		return ebook.Book{}, errors.New(analysis.Issues[0].Message)
	}
	return analysis.Book, nil
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
