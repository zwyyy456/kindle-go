package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/converter"
)

type ConvertOptions struct {
	RecordID         string
	Format           string
	Title            string
	Author           string
	Language         string
	H1Regex          string
	H2Regex          string
	SplitLevel       int
	LineHeight       float64
	ParagraphIndent  string
	ParagraphSpacing string
	TextAlign        string
}

func convertFile(ctx context.Context, inputPath, outputPath, inputFormat string, baseCfg txtconfig.Config, opts ConvertOptions) error {
	format := outputFormat(opts.Format)
	if format != "azw3" && format != "epub" {
		return fmt.Errorf("unsupported output format %q", opts.Format)
	}

	inputFormat = strings.ToLower(inputFormat)
	if inputFormat != "txt" && inputFormat != "epub" {
		return fmt.Errorf("%s files are not convertible", inputFormat)
	}

	cfg := configFor(inputPath, outputPath, baseCfg, opts)
	metadata := converter.MetadataOverrides{Title: cfg.Metadata.Title, Author: cfg.Metadata.Author, Language: cfg.Metadata.Language}
	if inputFormat == "epub" {
		metadata.Title = strings.TrimSpace(opts.Title)
		metadata.Author = strings.TrimSpace(opts.Author)
		metadata.Language = ""
	}
	_, err := converter.Convert(ctx, converter.Request{
		InputPath: inputPath, OutputPath: outputPath,
		InputFormat: converter.Format(inputFormat), OutputFormat: converter.Format(format),
		Metadata: metadata, DefaultLanguage: cfg.Metadata.Language,
		TXTConfig: cfg,
	})
	return err
}

func outputFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return "azw3"
	}
	return format
}

func configFor(inputPath, outputPath string, baseCfg txtconfig.Config, opts ConvertOptions) txtconfig.Config {
	cfg := baseCfg
	cfg.Output.Path = outputPath
	cfg.Output.Format = outputFormat(opts.Format)
	if strings.TrimSpace(opts.Title) != "" {
		cfg.Metadata.Title = strings.TrimSpace(opts.Title)
	} else {
		cfg.Metadata.Title = strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	}
	if strings.TrimSpace(opts.Author) != "" {
		cfg.Metadata.Author = strings.TrimSpace(opts.Author)
	}
	if strings.TrimSpace(opts.Language) != "" {
		cfg.Metadata.Language = strings.TrimSpace(opts.Language)
	}
	if strings.TrimSpace(opts.H1Regex) != "" {
		cfg.TXT.H1Regex = opts.H1Regex
	}
	if strings.TrimSpace(opts.H2Regex) != "" {
		cfg.TXT.H2Regex = opts.H2Regex
	}
	if opts.SplitLevel > 0 {
		cfg.TXT.SplitLevel = opts.SplitLevel
	}
	if opts.LineHeight > 0 {
		cfg.Style.LineHeight = opts.LineHeight
	}
	if strings.TrimSpace(opts.ParagraphIndent) != "" {
		cfg.Style.ParagraphIndent = strings.TrimSpace(opts.ParagraphIndent)
	}
	if strings.TrimSpace(opts.ParagraphSpacing) != "" {
		cfg.Style.ParagraphSpacing = strings.TrimSpace(opts.ParagraphSpacing)
	}
	if strings.TrimSpace(opts.TextAlign) != "" {
		cfg.Style.TextAlign = strings.TrimSpace(opts.TextAlign)
	}
	txtconfig.Normalize(&cfg)
	return cfg
}

func convertOptionsFromForm(get func(string) string) ConvertOptions {
	return ConvertOptions{
		RecordID:         get("record_id"),
		Format:           get("format"),
		Title:            get("title"),
		Author:           get("author"),
		Language:         get("language"),
		H1Regex:          get("h1_regex"),
		H2Regex:          get("h2_regex"),
		SplitLevel:       parseInt(get("split_level")),
		LineHeight:       parseFloat(get("line_height")),
		ParagraphIndent:  get("paragraph_indent"),
		ParagraphSpacing: get("paragraph_spacing"),
		TextAlign:        get("text_align"),
	}
}

func parseInt(raw string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(raw))
	return n
}

func parseFloat(raw string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return f
}

func outputFileName(inputName, format string, now time.Time) string {
	base := strings.TrimSuffix(inputName, filepath.Ext(inputName))
	if base == "" {
		base = "book"
	}
	suffix := now.Format("20060102-150405")
	return safeFileName(base + "-" + suffix + "." + strings.ToLower(format))
}
