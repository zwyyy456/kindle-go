package converter

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/azw3"
	"github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/ebook"
	"github.com/flashdict/kindle2flashdict/internal/epub"
	txtbook "github.com/flashdict/kindle2flashdict/internal/txt2epub/book"
	txttext "github.com/flashdict/kindle2flashdict/internal/txt2epub/text"
)

type TXTAnalysis struct {
	Charset string
	TOC     []ebook.TOCEntry
	Stats   txtbook.Stats
	Parsed  txtbook.Book
	Book    ebook.Book
}

func AnalyzeTXT(input string, cfg config.Config) (TXTAnalysis, error) {
	decoded, err := txttext.ReadFile(input)
	if err != nil {
		return TXTAnalysis{}, err
	}
	if strings.TrimSpace(cfg.Metadata.Title) == "" {
		cfg.Metadata.Title = strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	}
	config.Normalize(&cfg)
	parser, err := txtbook.NewParser(cfg)
	if err != nil {
		return TXTAnalysis{}, err
	}
	cleaner, err := txttext.NewCleaner(cfg)
	if err != nil {
		return TXTAnalysis{}, err
	}
	lines, stats := cleaner.Clean(decoded.Text, cfg, parser.IsHeading)
	stats.Charset = decoded.Charset
	parsed, err := parser.Build(lines, cfg, stats)
	if err != nil {
		return TXTAnalysis{}, err
	}
	book := txtbook.ToEBook(parsed)
	return TXTAnalysis{Charset: decoded.Charset, TOC: book.TOC, Stats: parsed.Stats, Parsed: parsed, Book: book}, nil
}

func WriteTXTAnalysis(output, format string, analysis TXTAnalysis) error {
	switch strings.ToLower(format) {
	case "epub":
		return epub.Write(output, analysis.Book)
	case "azw3":
		return azw3.Write(output, analysis.Book, azw3.Options{})
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}
