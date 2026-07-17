package proofread

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/ebook"
	"github.com/flashdict/kindle2flashdict/internal/epub"
	"github.com/flashdict/kindle2flashdict/internal/library"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

type fakeProofreadModel struct {
	mu                    sync.Mutex
	fail, proposeTXT      bool
	verificationVerdict   string
	active, maxConcurrent int
}

type blockingProofreadModel struct {
	started  chan struct{}
	canceled chan struct{}
}

func (m *blockingProofreadModel) Invoke(ctx context.Context, _ CodexRequest, _ any) error {
	select {
	case m.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	close(m.canceled)
	return ctx.Err()
}

func (m *fakeProofreadModel) Invoke(ctx context.Context, request CodexRequest, output any) error {
	m.mu.Lock()
	m.active++
	if m.active > m.maxConcurrent {
		m.maxConcurrent = m.active
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.active--
		m.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	if m.fail {
		return &CodexError{Code: "codex_unavailable", Message: "fake model failed"}
	}
	switch value := output.(type) {
	case *firstReviewOutput:
		if m.proposeTXT && strings.Contains(string(request.Prompt), "噗之以鼻") {
			value.Candidates = []reviewCandidate{{BatchID: "batch-00001", Line: 2, Occurrence: 1, Original: "噗之以鼻", Proposed: "嗤之以鼻", Category: "wrong_character", Confidence: "high", Reason: "固定成语", Context: "他对此噗之以鼻。"}}
		}
		value.Glossary = []glossaryProposal{}
	case *verificationOutput:
		value.Verdict = m.verificationVerdict
		if value.Verdict == "" {
			value.Verdict = "high"
		}
		value.Proposed = "嗤之以鼻"
		value.Reason = "上下文明确"
	case *imageReviewOutput:
		value.Candidates = nil
	default:
		return errors.New("unexpected fake output type")
	}
	return nil
}

func TestProofreadExecutorCompletesTXTAndPersistsVerifiedCandidates(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n他对此噗之以鼻。\n")})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage, libraryService, taskService, settingsService)
	created, err := service.Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeProofreadModel{proposeTXT: true}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, model), created.ID, task.Completed)
	runs, err := storage.ProofreadRuns(context.Background(), imported.Book.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "completed" || runs[0].SourceSHA256 != imported.Book.Original.SHA256 {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	candidates, err := storage.ProofreadCandidates(context.Background(), runs[0].ID)
	if err != nil || len(candidates) != 1 || candidates[0].ExpectedOriginal != "噗之以鼻" || candidates[0].FirstReplacement != "嗤之以鼻" || candidates[0].Verification != "high" || candidates[0].VerifiedReplacement != "嗤之以鼻" {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
	statePath, err := storage.ResolveRel(runs[0].EngineStateRelPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("persisted engine state = %v", err)
	}
	if version, err := os.ReadFile(filepath.Join(statePath, "engine", "ENGINE_VERSION")); err != nil || strings.TrimSpace(string(version)) != runs[0].EngineVersion {
		t.Fatalf("persisted engine version = %q, %v", version, err)
	}
}

func TestProofreadExecutorCompletesMinimalEPUB(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	data := minimalProofreadEPUB(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.epub", Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	created, err := NewService(storage, libraryService, taskService, settingsService).Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, &fakeProofreadModel{}), created.ID, task.Completed)
	runs, err := storage.ProofreadRuns(context.Background(), imported.Book.ID)
	if err != nil || len(runs) != 1 || runs[0].Format != "epub" {
		t.Fatalf("EPUB runs = %#v, %v", runs, err)
	}
}

func TestProofreadFailureDoesNotExposeRunOrCandidates(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n正文\n")})
	if err != nil {
		t.Fatal(err)
	}
	created, err := NewService(storage, libraryService, taskService, settingsService).Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, &fakeProofreadModel{fail: true}), created.ID, task.Failed)
	failed, _, err := taskService.Get(context.Background(), created.ID)
	if err != nil || failed.ErrorCode != "codex_unavailable" {
		t.Fatalf("failed task = %#v, %v", failed, err)
	}
	runs, err := storage.ProofreadRuns(context.Background(), imported.Book.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("failed runs = %#v, %v", runs, err)
	}
	retry, err := taskService.Retry(context.Background(), created.ID)
	if err != nil || retry.RetryOfTaskID != created.ID {
		t.Fatalf("retry = %#v, %v", retry, err)
	}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, &fakeProofreadModel{}), retry.ID, task.Completed)
	runs, err = storage.ProofreadRuns(context.Background(), imported.Book.ID)
	if err != nil || len(runs) != 1 || runs[0].TaskID != retry.ID {
		t.Fatalf("retried runs = %#v, %v", runs, err)
	}
}

func TestCancelProofreadStopsModelAndDoesNotExposeRun(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n正文\n")})
	if err != nil {
		t.Fatal(err)
	}
	created, err := NewService(storage, libraryService, taskService, settingsService).Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	model := &blockingProofreadModel{started: make(chan struct{}, 1), canceled: make(chan struct{})}
	runnerContext, stopRunner := context.WithCancel(context.Background())
	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- task.NewRunner(taskService, map[task.Type]task.Executor{task.Proofread: NewExecutor(storage, libraryService, nil, model)}).Run(runnerContext)
	}()
	select {
	case <-model.started:
	case <-time.After(10 * time.Second):
		stopRunner()
		t.Fatal("proofreading model did not start")
	}
	if err := taskService.Cancel(context.Background(), created.ID); err != nil {
		stopRunner()
		t.Fatal(err)
	}
	select {
	case <-model.canceled:
	case <-time.After(3 * time.Second):
		stopRunner()
		t.Fatal("proofreading model did not observe cancellation")
	}
	waitProofreadStatus(t, taskService, created.ID, task.Canceled)
	stopRunner()
	if err := <-runnerDone; err != nil {
		t.Fatal(err)
	}
	runs, err := storage.ProofreadRuns(context.Background(), imported.Book.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("canceled runs = %#v, %v", runs, err)
	}
}

func TestProofreadBatchesUseConfiguredInternalConcurrency(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	values, err := settingsService.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	values.Proofread.BatchSize = 1000
	values.Proofread.Concurrency = 2
	if err := settingsService.Save(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	content := "第一章\n" + strings.Repeat("正文内容。\n", 900)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "long.txt", Reader: strings.NewReader(content)})
	if err != nil {
		t.Fatal(err)
	}
	created, err := NewService(storage, libraryService, taskService, settingsService).Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeProofreadModel{}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, model), created.ID, task.Completed)
	model.mu.Lock()
	maxConcurrent := model.maxConcurrent
	model.mu.Unlock()
	if maxConcurrent < 2 {
		t.Fatalf("max concurrent model calls = %d", maxConcurrent)
	}
}

func newProofreadTest(t *testing.T) (*store.Store, *library.Service, *appsettings.Service, *task.Service) {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	libraryService := library.New(storage)
	t.Cleanup(func() { _ = libraryService.Close() })
	settingsService := appsettings.New(storage, txtconfig.Defaults(), appsettings.Runtime{})
	if err := settingsService.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return storage, libraryService, settingsService, task.NewService(storage)
}

func runProofreadTask(t *testing.T, taskService *task.Service, executor *Executor, id string, want task.Status) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- task.NewRunner(taskService, map[task.Type]task.Executor{task.Proofread: executor}).Run(ctx)
	}()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		value, ok, err := taskService.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && value.Status == want {
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	value, _, _ := taskService.Get(context.Background(), id)
	cancel()
	<-done
	t.Fatalf("task = %#v, want %s", value, want)
}

func waitProofreadStatus(t *testing.T, taskService *task.Service, id string, want task.Status) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value, ok, err := taskService.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && value.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	value, _, _ := taskService.Get(context.Background(), id)
	t.Fatalf("task = %#v, want %s", value, want)
}

func minimalProofreadEPUB(t *testing.T) []byte {
	t.Helper()
	filename := t.TempDir() + "/book.epub"
	book := ebook.Book{
		Metadata: ebook.Metadata{Title: "测试", Language: "zh-CN"},
		Spine:    []ebook.Document{{Href: "chapter.xhtml", Title: "正文", Body: ebook.Element("body", nil, ebook.Element("p", nil, ebook.Text("正文内容。")))}},
		TOC:      []ebook.TOCEntry{{Title: "正文", Href: "chapter.xhtml"}},
	}
	if err := epub.Write(filename, book); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
