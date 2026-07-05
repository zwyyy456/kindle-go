package server

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	txtapp "github.com/flashdict/kindle2flashdict/internal/txt2epub/app"
	"github.com/flashdict/kindle2flashdict/internal/txt2epub/calibre"
	txtconfig "github.com/flashdict/kindle2flashdict/internal/txt2epub/config"
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

func convertFile(inputPath, outputPath, inputFormat string, baseCfg txtconfig.Config, opts ConvertOptions) error {
	format := outputFormat(opts.Format)
	if format != "azw3" && format != "epub" {
		return fmt.Errorf("unsupported output format %q", opts.Format)
	}

	inputFormat = strings.ToLower(inputFormat)
	if inputFormat == "epub" && format != "azw3" {
		return fmt.Errorf("epub input can only be converted to azw3")
	}
	if inputFormat != "txt" && inputFormat != "epub" {
		return fmt.Errorf("%s files are not convertible", inputFormat)
	}

	cfg := configFor(inputPath, outputPath, baseCfg, opts)
	if inputFormat == "txt" {
		return txtapp.Run(inputPath, cfg, txtapp.Options{}, io.Discard)
	} else {
		return calibre.Convert(inputPath, outputPath, cfg)
	}
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
	cfg.Output = outputPath
	cfg.Format = outputFormat(opts.Format)
	if strings.TrimSpace(opts.Title) != "" {
		cfg.Title = strings.TrimSpace(opts.Title)
	} else {
		cfg.Title = strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	}
	if strings.TrimSpace(opts.Author) != "" {
		cfg.Author = strings.TrimSpace(opts.Author)
	}
	if strings.TrimSpace(opts.Language) != "" {
		cfg.Language = strings.TrimSpace(opts.Language)
	}
	if strings.TrimSpace(opts.H1Regex) != "" {
		cfg.H1Regex = opts.H1Regex
	}
	if strings.TrimSpace(opts.H2Regex) != "" {
		cfg.H2Regex = opts.H2Regex
	}
	if opts.SplitLevel > 0 {
		cfg.SplitLevel = opts.SplitLevel
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
