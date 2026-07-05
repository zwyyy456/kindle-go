package app

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/azw3"
	"github.com/flashdict/kindle2flashdict/internal/txt2epub/book"
	"github.com/flashdict/kindle2flashdict/internal/txt2epub/config"
	"github.com/flashdict/kindle2flashdict/internal/txt2epub/epub"
	txt "github.com/flashdict/kindle2flashdict/internal/txt2epub/text"
)

type Options struct {
	ConfigPath string
	Preview    bool
	Verbose    bool
}

func Run(input string, cfg config.Config, opts Options, stdout io.Writer) error {
	decoded, err := txt.ReadFile(input)
	if err != nil {
		return err
	}

	if cfg.Title == "" {
		cfg.Title = strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	}
	config.Normalize(&cfg)

	parser, err := book.NewParser(cfg)
	if err != nil {
		return err
	}
	cleaner, err := txt.NewCleaner(cfg)
	if err != nil {
		return err
	}
	lines, textStats := cleaner.Clean(decoded.Text, cfg, parser.IsHeading)
	textStats.Charset = decoded.Charset

	b, err := parser.Build(lines, cfg, textStats)
	if err != nil {
		return err
	}

	if opts.Preview {
		book.PrintPreview(stdout, b)
		return nil
	}

	output := config.OutputPath(input, cfg)
	format := cfg.Format
	if strings.EqualFold(filepath.Ext(output), ".azw3") {
		format = "azw3"
	}
	switch format {
	case "epub":
		if err := epub.Write(output, b); err != nil {
			return err
		}
	case "azw3":
		if err := azw3.Write(output, book.ToEBook(b), azw3.Options{}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported format %q", cfg.Format)
	}

	if opts.Verbose {
		book.PrintPreview(stdout, b)
	} else {
		book.PrintSummary(stdout, b, output)
	}
	return nil
}
