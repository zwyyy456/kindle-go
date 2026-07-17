package app

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/converter"
	"github.com/flashdict/kindle2flashdict/internal/txt2epub/book"
)

type Options struct {
	ConfigPath string
	Preview    bool
	Verbose    bool
}

func Run(input string, cfg config.Config, opts Options, stdout io.Writer) error {
	config.Normalize(&cfg)
	analysis, err := converter.AnalyzeTXT(input, cfg)
	if err != nil {
		return err
	}

	if opts.Preview {
		book.PrintPreview(stdout, analysis.Parsed)
		return nil
	}

	output := config.OutputPath(input, cfg)
	format := cfg.Output.Format
	if strings.EqualFold(filepath.Ext(output), ".azw3") {
		format = "azw3"
	}
	if err := converter.WriteTXTAnalysis(output, format, analysis); err != nil {
		return err
	}

	if opts.Verbose {
		book.PrintPreview(stdout, analysis.Parsed)
	} else {
		book.PrintSummary(stdout, analysis.Parsed, output)
	}
	return nil
}
