package proofread

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/task"
	epubengine "github.com/flashdict/kindle2flashdict/long-epub-proofreader"
	novelengine "github.com/flashdict/kindle2flashdict/long-novel-proofreader"
)

type Parameters struct {
	ExpectedSHA256 string `json:"expected_sha256"`
	Format         string `json:"format"`
	Model          string `json:"model"`
	BatchSize      int    `json:"batch_size"`
	Concurrency    int    `json:"concurrency"`
	EngineVersion  string `json:"engine_version"`
}

type Service struct {
	library  *library.Service
	tasks    *task.Service
	settings *settings.Service
	now      func() time.Time
}

func NewService(libraryService *library.Service, taskService *task.Service, settingsService *settings.Service) *Service {
	return &Service{library: libraryService, tasks: taskService, settings: settingsService, now: time.Now}
}

func (s *Service) Create(ctx context.Context, bookID string) (task.Task, error) {
	book, ok, err := s.library.GetBook(ctx, bookID)
	if err != nil {
		return task.Task{}, err
	}
	if !ok || (book.SourceFormat != "txt" && book.SourceFormat != "epub") {
		return task.Task{}, fmt.Errorf("proofreading is only available for TXT and EPUB books")
	}
	existingTasks, err := s.tasks.List(ctx, bookID)
	if err != nil {
		return task.Task{}, err
	}
	for _, existing := range existingTasks {
		if existing.Type == task.Proofread && (existing.Status == task.Queued || existing.Status == task.Running) {
			return task.Task{}, fmt.Errorf("book already has an active proofreading task")
		}
	}
	values, err := s.settings.Current(ctx)
	if err != nil {
		return task.Task{}, err
	}
	version := novelengine.Version()
	if book.SourceFormat == "epub" {
		version = epubengine.Version()
	}
	parameters := Parameters{
		ExpectedSHA256: book.Original.SHA256, Format: book.SourceFormat, Model: values.Proofread.Model,
		BatchSize: values.Proofread.BatchSize, Concurrency: values.Proofread.Concurrency, EngineVersion: version,
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return task.Task{}, err
	}
	return s.tasks.Create(ctx, task.CreateRequest{BookID: book.ID, Type: task.Proofread, InputFileID: book.Original.ID, ParametersJSON: string(encoded), CreatedAt: s.now()})
}
