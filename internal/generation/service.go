package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/converter"
	"github.com/flashdict/kindle2flashdict/internal/library"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

type Options struct {
	Title            string
	Author           string
	Language         string
	H1Regex          string
	H2Regex          string
	DropRegex        *[]string
	Replace          *[]txtconfig.ReplaceRule
	SplitLevel       int
	LineHeight       float64
	ParagraphIndent  string
	ParagraphSpacing string
	TextAlign        string
	Cover            *bool
	MergeLines       *bool
	TrimBlankLines   *bool
}

type PreviewRequest struct {
	BookID      string
	InputFileID string
	Options     Options
}

type InputAssessment struct {
	ID                  string
	Name                string
	Role                string
	HasUnresolved       bool
	Available           bool
	CompatibilityStatus string
}

type UserError struct {
	err error
}

func (e *UserError) Error() string { return e.err.Error() }

func (e *UserError) Unwrap() error { return e.err }

func userErrorf(format string, args ...any) error {
	return &UserError{err: fmt.Errorf(format, args...)}
}

func (s *Service) PreviewTXT(ctx context.Context, req PreviewRequest) (converter.TXTAnalysis, error) {
	input, err := s.resolveInput(ctx, req.BookID, req.InputFileID)
	if err != nil {
		return converter.TXTAnalysis{}, err
	}
	if input.Format != "txt" {
		return converter.TXTAnalysis{}, userErrorf("TXT preview is only available for TXT books")
	}
	path, _, err := s.library.ResolveInput(ctx, req.BookID, input.ID, input.SHA256)
	if err != nil {
		return converter.TXTAnalysis{}, err
	}
	params, err := s.parameters(ctx, input, "epub", req.Options)
	if err != nil {
		return converter.TXTAnalysis{}, err
	}
	cfg := txtconfig.Defaults()
	cfg.Metadata = params.Metadata
	cfg.TXT = params.TXT
	cfg.Style = params.Style
	cfg.Output.Cover = params.Cover
	return converter.AnalyzeTXT(path, cfg)
}

func (s *Service) Inputs(ctx context.Context, bookID string) ([]InputAssessment, error) {
	detail, ok, err := s.library.GetBookDetail(ctx, bookID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, userErrorf("book %q not found", bookID)
	}
	inputs := make([]InputAssessment, 0, len(detail.Files))
	for _, file := range detail.Files {
		if file.Role != "original" && file.Role != "revision" {
			continue
		}
		input, err := s.assessInput(ctx, file)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

type CreateRequest struct {
	BookID      string
	InputFileID string
	Formats     []string
	Options     Options
}

type Parameters struct {
	SchemaVersion   int                      `json:"version"`
	InputFormat     string                   `json:"input_format"`
	OutputFormat    string                   `json:"output_format"`
	ExpectedSHA256  string                   `json:"expected_sha256"`
	Metadata        txtconfig.MetadataConfig `json:"metadata"`
	TXT             txtconfig.TXTConfig      `json:"txt"`
	Style           txtconfig.Style          `json:"style"`
	Cover           bool                     `json:"cover"`
	DefaultLanguage string                   `json:"default_language"`
}

type Service struct {
	library  *library.Service
	tasks    *task.Service
	settings *appsettings.Service
	now      func() time.Time
}

func NewService(libraryService *library.Service, taskService *task.Service, settingsService *appsettings.Service) *Service {
	if libraryService == nil || taskService == nil || settingsService == nil {
		panic("generation.NewService requires library, task, and settings services")
	}
	return &Service{library: libraryService, tasks: taskService, settings: settingsService, now: time.Now}
}

func (s *Service) Create(ctx context.Context, req CreateRequest) ([]task.Task, error) {
	input, err := s.resolveInput(ctx, req.BookID, req.InputFileID)
	if err != nil {
		return nil, err
	}
	assessment, err := s.assessInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if !assessment.Available {
		if input.Format == "epub" {
			return nil, userErrorf("epub_incompatible: EPUB compatibility report did not pass")
		}
		return nil, userErrorf("%s files are not convertible", input.Format)
	}
	if len(req.Formats) == 0 {
		return nil, userErrorf("at least one output format is required")
	}
	createdAt := s.now()
	seen := make(map[string]bool)
	requests := make([]task.CreateRequest, 0, len(req.Formats))
	for _, rawFormat := range req.Formats {
		format := strings.ToLower(strings.TrimSpace(rawFormat))
		if seen[format] {
			return nil, userErrorf("duplicate output format %q", format)
		}
		seen[format] = true
		if format != "epub" && format != "azw3" {
			return nil, userErrorf("unsupported output format %q", format)
		}
		if input.Format == "epub" && format != "azw3" {
			return nil, userErrorf("EPUB input can only generate AZW3")
		}
		parameters, err := s.parameters(ctx, input, format, req.Options)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(parameters)
		if err != nil {
			return nil, err
		}
		taskType := task.GenerateEPUB
		if format == "azw3" {
			taskType = task.GenerateAZW3
		}
		requests = append(requests, task.CreateRequest{BookID: req.BookID, Type: taskType, InputFileID: input.ID, ParametersJSON: string(encoded), CreatedAt: createdAt})
	}
	return s.tasks.CreateMany(ctx, requests)
}

func (s *Service) resolveInput(ctx context.Context, bookID, inputFileID string) (library.File, error) {
	book, ok, err := s.library.GetBook(ctx, bookID)
	if err != nil {
		return library.File{}, err
	}
	if !ok {
		return library.File{}, userErrorf("book %q not found", bookID)
	}
	if strings.TrimSpace(inputFileID) == "" {
		return book.Original, nil
	}
	input, ok, err := s.library.GetFile(ctx, inputFileID)
	if err != nil {
		return library.File{}, err
	}
	if !ok || input.BookID != book.ID || (input.Role != "original" && input.Role != "revision") {
		return library.File{}, userErrorf("generation input file not found")
	}
	return input, nil
}

func (s *Service) assessInput(ctx context.Context, input library.File) (InputAssessment, error) {
	assessment := InputAssessment{
		ID: input.ID, Name: input.DisplayName, Role: input.Role, HasUnresolved: input.HasUnresolved,
	}
	switch input.Format {
	case "txt":
		assessment.Available = true
	case "epub":
		report, found, err := s.library.CompatibilityForFile(ctx, input.ID)
		if err != nil {
			return InputAssessment{}, err
		}
		if found {
			assessment.CompatibilityStatus = report.Status
			assessment.Available = report.Status == "passed"
		}
	}
	return assessment, nil
}

func (s *Service) parameters(ctx context.Context, input library.File, format string, opts Options) (Parameters, error) {
	cfg, err := s.settings.ConversionConfig(ctx)
	if err != nil {
		return Parameters{}, err
	}
	cfg.Metadata.Title = firstNonBlank(opts.Title, strings.TrimSuffix(input.DisplayName, filepath.Ext(input.DisplayName)))
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
	if opts.DropRegex != nil {
		cfg.TXT.DropRegex = append([]string(nil), (*opts.DropRegex)...)
	}
	if opts.Replace != nil {
		cfg.TXT.Replace = append([]txtconfig.ReplaceRule(nil), (*opts.Replace)...)
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
	if opts.Cover != nil {
		cfg.Output.Cover = *opts.Cover
	}
	if opts.MergeLines != nil {
		cfg.TXT.MergeLines = *opts.MergeLines
	}
	if opts.TrimBlankLines != nil {
		cfg.TXT.TrimBlankLines = *opts.TrimBlankLines
	}
	txtconfig.Normalize(&cfg)
	metadata := cfg.Metadata
	if input.Format == "epub" {
		metadata.Language = ""
	}
	return Parameters{
		SchemaVersion: taskParametersVersion,
		InputFormat:   input.Format, OutputFormat: format, ExpectedSHA256: input.SHA256,
		Metadata: metadata, TXT: cfg.TXT, Style: cfg.Style, Cover: cfg.Output.Cover,
		DefaultLanguage: cfg.Metadata.Language,
	}, nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "book"
}
