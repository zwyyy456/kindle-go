package converter

import (
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/azw3"
	"github.com/flashdict/kindle2flashdict/internal/epub"
)

type EPUBOptions struct {
	DefaultLanguage string
	Title           string
	Author          string
}

func EPUBToAZW3(inputPath, outputPath string, opts EPUBOptions) error {
	book, err := epub.Read(inputPath, epub.Options{DefaultLanguage: opts.DefaultLanguage})
	if err != nil {
		return err
	}
	if title := strings.TrimSpace(opts.Title); title != "" {
		book.Metadata.Title = title
	}
	if author := strings.TrimSpace(opts.Author); author != "" {
		book.Metadata.Author = author
	}
	return azw3.Write(outputPath, book, azw3.Options{})
}
