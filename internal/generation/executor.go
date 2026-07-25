package generation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/converter"
	"github.com/flashdict/kindle2flashdict/internal/epub"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

type Executor struct {
	library *library.Service
}

func NewExecutor(service *library.Service) *Executor { return &Executor{library: service} }

func (e *Executor) Execute(ctx context.Context, value task.Task, progress task.ProgressReporter) error {
	params, err := decodeParameters(value.ParametersJSON)
	if err != nil {
		return &task.ExecutionError{Code: "invalid_task_parameters", Err: err}
	}
	if err := progress.Report(ctx, "prepare", 0, 4); err != nil {
		return err
	}
	inputPath, input, err := e.library.ResolveInput(ctx, value.BookID, value.InputFileID, params.ExpectedSHA256)
	if err != nil {
		return &task.ExecutionError{Code: errorCode(err, "source_unavailable"), Err: err}
	}
	outputPath, workRelPath, err := e.library.WorkOutputPath(value.ID, params.OutputFormat)
	if err != nil {
		return err
	}
	defer e.library.RemoveTaskWork(value.ID)
	if params.InputFormat == "epub" {
		analysis := epub.Analyze(inputPath, epub.Options{DefaultLanguage: params.DefaultLanguage})
		if !analysis.Compatible() {
			return &task.ExecutionError{Code: "epub_incompatible", Message: analysis.Issues[0].Message}
		}
	}
	if err := progress.Report(ctx, "parse", 1, 4); err != nil {
		return err
	}
	cfg := txtconfig.Defaults()
	cfg.Metadata = params.Metadata
	cfg.TXT = params.TXT
	cfg.Style = params.Style
	cfg.Output.Cover = params.Cover
	metadata := converter.MetadataOverrides{Title: params.Metadata.Title, Author: params.Metadata.Author, Language: params.Metadata.Language}
	err = converter.Convert(ctx, converter.Request{
		InputPath: inputPath, OutputPath: outputPath,
		InputFormat: converter.Format(params.InputFormat), OutputFormat: converter.Format(params.OutputFormat),
		Metadata: metadata, DefaultLanguage: params.DefaultLanguage, TXTConfig: cfg,
	})
	if err != nil {
		_ = os.Remove(outputPath)
		return &task.ExecutionError{Code: "conversion_failed", Err: err}
	}
	if err := progress.Report(ctx, "write", 3, 4); err != nil {
		return err
	}
	if err := progress.Report(ctx, "finalize", 4, 4); err != nil {
		return err
	}
	name := outputName(input.DisplayName, params.OutputFormat, value)
	if _, err := e.library.CommitGeneratedArtifact(ctx, library.GeneratedArtifact{
		BookID: value.BookID, SourceFileID: value.InputFileID, TaskID: value.ID,
		Format: params.OutputFormat, DisplayName: name, WorkRelPath: workRelPath,
		ParametersJSON: value.ParametersJSON, CreatedAt: value.CreatedAt,
	}); err != nil {
		return &task.ExecutionError{Code: "artifact_commit_failed", Err: err}
	}
	return nil
}

func outputName(inputName, format string, value task.Task) string {
	base := strings.TrimSuffix(filepath.Base(inputName), filepath.Ext(inputName))
	if base == "" {
		base = "book"
	}
	return safeName(fmt.Sprintf("%s-%s.%s", base, value.CreatedAt.Local().Format("20060102-150405"), format))
}

func safeName(name string) string {
	var result strings.Builder
	for _, value := range name {
		if value < 32 || value == '/' || value == '\\' || value == 0 {
			result.WriteRune('_')
		} else {
			result.WriteRune(value)
		}
	}
	return result.String()
}

func errorCode(err error, fallback string) string {
	if strings.Contains(err.Error(), "source_hash_mismatch") {
		return "source_hash_mismatch"
	}
	return fallback
}
