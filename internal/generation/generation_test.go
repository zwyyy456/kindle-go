package generation

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/library"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestCreateSnapshotsParametersAndRunnerGeneratesBothFormats(t *testing.T) {
	_, libraryService, taskService, settingsService, book := newGenerationTest(t)
	service := NewService(libraryService, taskService, settingsService)
	tasks, err := service.Create(context.Background(), CreateRequest{
		BookID: book.ID, Formats: []string{"epub", "azw3"},
		Options: Options{Title: "任务标题", Author: "作者", SplitLevel: 1, LineHeight: 1.8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].QueueSeq+1 != tasks[1].QueueSeq {
		t.Fatalf("tasks = %#v", tasks)
	}
	var snapshot Parameters
	if err := json.Unmarshal([]byte(tasks[0].ParametersJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != taskParametersVersion || snapshot.Metadata.Title != "任务标题" || snapshot.Metadata.Author != "作者" || snapshot.TXT.SplitLevel != 1 || snapshot.Style.LineHeight != 1.8 || snapshot.ExpectedSHA256 != book.Original.SHA256 {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runnerDone := make(chan error, 1)
	executor := NewExecutor(libraryService)
	runner := task.NewRunner(taskService, map[task.Type]task.Executor{task.GenerateEPUB: executor, task.GenerateAZW3: executor})
	go func() { runnerDone <- runner.Run(ctx) }()
	for _, value := range tasks {
		waitGenerationStatus(t, taskService, value.ID, task.Completed)
	}
	loaded, ok, err := libraryService.GetBook(context.Background(), book.ID)
	if err != nil || !ok || loaded.LatestArtifact.ID == "" {
		t.Fatalf("book after generation = %#v, %v, %v", loaded, ok, err)
	}
	detail, ok, err := libraryService.GetBookDetail(context.Background(), book.ID)
	if err != nil || !ok {
		t.Fatalf("detail = %#v, %v, %v", detail, ok, err)
	}
	artifacts := 0
	formats := map[string]bool{}
	for _, file := range detail.Files {
		if file.Role == "artifact" {
			artifacts++
			formats[file.Format] = true
			if file.TaskID == "" || file.ParametersJSON == "" {
				t.Fatalf("artifact provenance = %#v", file)
			}
		}
	}
	if artifacts != 2 || !formats["epub"] || !formats["azw3"] {
		t.Fatalf("artifact formats = %#v in %#v", formats, detail.Files)
	}
	kindle, err := libraryService.LatestKindleFiles(context.Background(), false)
	if err != nil || len(kindle) != 1 || len(kindle[0].Files) != 1 || kindle[0].Files[0].Format != "azw3" {
		t.Fatalf("Kindle projection = %#v, %v", kindle, err)
	}
	if _, downloaded, err := libraryService.DownloadKindleFile(context.Background(), kindle[0].Files[0].ID, false); err != nil || downloaded.ID != kindle[0].Files[0].ID {
		t.Fatalf("Kindle AZW3 download = %#v, %v", downloaded, err)
	}
	kindleWithEPUB, err := libraryService.LatestKindleFiles(context.Background(), true)
	if err != nil || len(kindleWithEPUB) != 1 || len(kindleWithEPUB[0].Files) != 2 || kindleWithEPUB[0].Files[0].Format != "azw3" || kindleWithEPUB[0].Files[1].Format != "epub" {
		t.Fatalf("Kindle EPUB projection = %#v, %v", kindleWithEPUB, err)
	}
	firstEPUBID := kindleWithEPUB[0].Files[1].ID
	again, err := service.Create(context.Background(), CreateRequest{BookID: book.ID, Formats: []string{"epub"}, Options: Options{Title: "第二版"}})
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationStatus(t, taskService, again[0].ID, task.Completed)
	detail, _, err = libraryService.GetBookDetail(context.Background(), book.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifacts = 0
	for _, file := range detail.Files {
		if file.Role == "artifact" {
			artifacts++
		}
	}
	if artifacts != 3 {
		t.Fatalf("repeated generation retained %d artifacts, want 3", artifacts)
	}
	kindleWithEPUB, err = libraryService.LatestKindleFiles(context.Background(), true)
	if err != nil || len(kindleWithEPUB) != 1 || len(kindleWithEPUB[0].Files) != 2 || kindleWithEPUB[0].Files[1].ID == firstEPUBID {
		t.Fatalf("updated Kindle EPUB projection = %#v, %v", kindleWithEPUB, err)
	}
	if _, _, err := libraryService.DownloadKindleFile(context.Background(), firstEPUBID, true); !os.IsNotExist(err) {
		t.Fatalf("historical EPUB remained downloadable from Kindle listener: %v", err)
	}
	cancel()
	select {
	case err := <-runnerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestQueuedGenerationFreezesGlobalDefaults(t *testing.T) {
	_, libraryService, taskService, settingsService, book := newGenerationTest(t)
	service := NewService(libraryService, taskService, settingsService)
	values, err := settingsService.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	values.Style.LineHeight = 1.9
	if err := settingsService.Save(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	first, err := service.Create(context.Background(), CreateRequest{BookID: book.ID, Formats: []string{"epub"}})
	if err != nil {
		t.Fatal(err)
	}
	values.Style.LineHeight = 2.2
	if err := settingsService.Save(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), CreateRequest{BookID: book.ID, Formats: []string{"epub"}})
	if err != nil {
		t.Fatal(err)
	}
	var firstParams, secondParams Parameters
	if err := json.Unmarshal([]byte(first[0].ParametersJSON), &firstParams); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(second[0].ParametersJSON), &secondParams); err != nil {
		t.Fatal(err)
	}
	if firstParams.Style.LineHeight != 1.9 || secondParams.Style.LineHeight != 2.2 {
		t.Fatalf("snapshots = %#v, %#v", firstParams.Style, secondParams.Style)
	}
}

func TestGenerationFailsWhenImmutableSourceHashChanges(t *testing.T) {
	_, libraryService, taskService, settingsService, book := newGenerationTest(t)
	service := NewService(libraryService, taskService, settingsService)
	tasks, err := service.Create(context.Background(), CreateRequest{BookID: book.ID, Formats: []string{"epub"}})
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := libraryService.ResolveOriginal(context.Background(), book.ID, book.Original.ID, book.Original.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	executor := NewExecutor(libraryService)
	go func() {
		done <- task.NewRunner(taskService, map[task.Type]task.Executor{task.GenerateEPUB: executor}).Run(ctx)
	}()
	failed := waitGenerationStatus(t, taskService, tasks[0].ID, task.Failed)
	if failed.ErrorCode != "source_hash_mismatch" {
		t.Fatalf("failed task = %#v", failed)
	}
	loaded, _, err := libraryService.GetBook(context.Background(), book.ID)
	if err != nil || loaded.LatestArtifact.ID != "" {
		t.Fatalf("failed task exposed artifact: %#v, %v", loaded.LatestArtifact, err)
	}
	cancel()
	<-done
}

func TestGenerationCanUseReadyRevisionAsImmutableInput(t *testing.T) {
	storage, libraryService, taskService, settingsService, book := newGenerationTest(t)
	revisionTask, err := taskService.Create(context.Background(), task.CreateRequest{BookID: book.ID, Type: task.BuildRevisionTXT, InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{string(task.BuildRevisionTXT)}, time.Now()); err != nil || !ok {
		t.Fatalf("claim revision = %v, %v", ok, err)
	}
	workRel := filepath.ToSlash(filepath.Join("work", revisionTask.ID, "revision-output"))
	workPath, err := storage.ResolveRel(workRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"revision.txt": "第一章 开始\n\n修订正文。", "report.md": "报告", "audit.jsonl": "{}\n"} {
		if err := os.WriteFile(filepath.Join(workPath, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := storage.CommitRevision(context.Background(), store.RevisionCommit{
		BookID: book.ID, SourceFileID: book.Original.ID, TaskID: revisionTask.ID, Format: "txt",
		RevisionName: "book-revised.txt", ReportName: "report.md", AuditName: "audit.jsonl", WorkDirRel: workRel,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(libraryService, taskService, settingsService)
	inputs, err := service.Inputs(context.Background(), book.ID)
	if err != nil {
		t.Fatal(err)
	}
	available := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		available[input.ID] = input.Available
	}
	if len(inputs) != 2 || !available[book.Original.ID] || !available[committed.Revision.ID] {
		t.Fatalf("generation inputs = %#v", inputs)
	}
	dropRegex := []string{`^修订正文。$`}
	analysis, err := service.PreviewTXT(context.Background(), PreviewRequest{
		BookID: book.ID, InputFileID: committed.Revision.ID, Options: Options{DropRegex: &dropRegex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Stats.Text.DroppedLines != 1 {
		t.Fatalf("revision preview stats = %#v", analysis.Stats)
	}
	created, err := service.Create(context.Background(), CreateRequest{BookID: book.ID, InputFileID: committed.Revision.ID, Formats: []string{"epub"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].InputFileID != committed.Revision.ID {
		t.Fatalf("generation task = %#v", created)
	}
	var params Parameters
	if err := json.Unmarshal([]byte(created[0].ParametersJSON), &params); err != nil {
		t.Fatal(err)
	}
	if params.ExpectedSHA256 != committed.Revision.SHA256 || params.InputFormat != "txt" {
		t.Fatalf("revision generation parameters = %#v", params)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- task.NewRunner(taskService, map[task.Type]task.Executor{task.GenerateEPUB: NewExecutor(libraryService)}).Run(ctx)
	}()
	waitGenerationStatus(t, taskService, created[0].ID, task.Completed)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEPUBGenerationRequiresPersistedReportAndRechecksSource(t *testing.T) {
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	libraryService := library.New(storage)
	defer libraryService.Close()
	taskService := task.NewService(storage)
	settingsService := appsettings.New(storage, txtconfig.Defaults(), appsettings.Runtime{})
	if err := settingsService.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := NewService(libraryService, taskService, settingsService)
	valid := generationEPUB(t, false)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "valid.epub", Reader: bytes.NewReader(valid)})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := service.Create(context.Background(), CreateRequest{BookID: imported.Book.ID, Formats: []string{"azw3"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	executor := NewExecutor(libraryService)
	go func() {
		done <- task.NewRunner(taskService, map[task.Type]task.Executor{task.GenerateAZW3: executor}).Run(ctx)
	}()
	waitGenerationStatus(t, taskService, tasks[0].ID, task.Completed)
	cancel()
	<-done

	invalid, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "invalid.epub", Reader: bytes.NewReader(generationEPUB(t, true))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), CreateRequest{BookID: invalid.Book.ID, Formats: []string{"azw3"}}); err == nil || !strings.Contains(err.Error(), "epub_incompatible") {
		t.Fatalf("incompatible create error = %v", err)
	}
}

func generationEPUB(t *testing.T, broken bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>EPUB</dc:title></metadata><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`,
	}
	if broken {
		delete(files, "c.xhtml")
	}
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func newGenerationTest(t *testing.T) (*store.Store, *library.Service, *task.Service, *appsettings.Service, library.Book) {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	libraryService := library.New(storage)
	t.Cleanup(func() { _ = libraryService.Close() })
	result, err := libraryService.Import(context.Background(), library.ImportRequest{
		Filename: "book.txt", Reader: strings.NewReader("第一章 开始\n\n正文。"), Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	settingsService := appsettings.New(storage, txtconfig.Defaults(), appsettings.Runtime{})
	if err := settingsService.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return storage, libraryService, task.NewService(storage), settingsService, result.Book
}

func waitGenerationStatus(t *testing.T, service *task.Service, id string, want task.Status) task.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, ok, err := service.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && value.Status == want {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	value, _, _ := service.Get(context.Background(), id)
	t.Fatalf("task = %#v, want %s", value, want)
	return task.Task{}
}
