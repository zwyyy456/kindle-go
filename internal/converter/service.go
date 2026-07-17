package converter

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/azw3"
	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/epub"
	txtapp "github.com/flashdict/kindle2flashdict/internal/txt2epub/app"
)

type Format string

const (
	FormatTXT  Format = "txt"
	FormatEPUB Format = "epub"
	FormatAZW3 Format = "azw3"
)

type MetadataOverrides struct {
	Title    string
	Author   string
	Language string
}

type Request struct {
	InputPath       string
	OutputPath      string
	InputFormat     Format
	OutputFormat    Format
	Metadata        MetadataOverrides
	DefaultLanguage string
	TXTConfig       txtconfig.Config
	Preview         bool
	Verbose         bool
	Stdout          io.Writer
}

type Result struct {
	OutputPath string
}

func Convert(ctx context.Context, req Request) (Result, error) {
	if err := validate(req); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	out := req.Stdout
	if out == nil {
		out = io.Discard
	}

	switch req.InputFormat {
	case FormatTXT:
		cfg := req.TXTConfig
		cfg.Output.Path = req.OutputPath
		cfg.Output.Format = string(req.OutputFormat)
		applyMetadata(&cfg, req.Metadata)
		txtconfig.Normalize(&cfg)
		if err := txtapp.Run(req.InputPath, cfg, txtapp.Options{Preview: req.Preview, Verbose: req.Verbose}, out); err != nil {
			return Result{}, err
		}
	case FormatEPUB:
		book, err := epub.Read(req.InputPath, epub.Options{DefaultLanguage: req.DefaultLanguage})
		if err != nil {
			return Result{}, err
		}
		if value := strings.TrimSpace(req.Metadata.Title); value != "" {
			book.Metadata.Title = value
		}
		if value := strings.TrimSpace(req.Metadata.Author); value != "" {
			book.Metadata.Author = value
		}
		if value := strings.TrimSpace(req.Metadata.Language); value != "" {
			book.Metadata.Language = value
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if err := azw3.Write(req.OutputPath, book, azw3.Options{}); err != nil {
			return Result{}, err
		}
	}
	return Result{OutputPath: req.OutputPath}, nil
}

func validate(req Request) error {
	if strings.TrimSpace(req.InputPath) == "" {
		return fmt.Errorf("input path is required")
	}
	if req.InputFormat != FormatTXT && req.InputFormat != FormatEPUB {
		return fmt.Errorf("unsupported input format %q", req.InputFormat)
	}
	if req.OutputFormat != FormatEPUB && req.OutputFormat != FormatAZW3 {
		return fmt.Errorf("unsupported output format %q", req.OutputFormat)
	}
	if req.InputFormat == FormatEPUB && req.OutputFormat != FormatAZW3 {
		return fmt.Errorf("epub input can only be converted to azw3")
	}
	if req.Preview && req.InputFormat != FormatTXT {
		return fmt.Errorf("preview is only supported for TXT input")
	}
	if !req.Preview && strings.TrimSpace(req.OutputPath) == "" {
		return fmt.Errorf("output path is required")
	}
	extension := strings.ToLower(filepath.Ext(req.OutputPath))
	wantExtension := "." + string(req.OutputFormat)
	stagedExtension := wantExtension + ".part"
	if !req.Preview && extension != wantExtension && !strings.HasSuffix(strings.ToLower(req.OutputPath), stagedExtension) {
		return fmt.Errorf("%s output must use .%s extension", strings.ToUpper(string(req.OutputFormat)), req.OutputFormat)
	}
	return nil
}

func applyMetadata(cfg *txtconfig.Config, metadata MetadataOverrides) {
	if value := strings.TrimSpace(metadata.Title); value != "" {
		cfg.Metadata.Title = value
	}
	if value := strings.TrimSpace(metadata.Author); value != "" {
		cfg.Metadata.Author = value
	}
	if value := strings.TrimSpace(metadata.Language); value != "" {
		cfg.Metadata.Language = value
	}
}
