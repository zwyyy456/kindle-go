package generation

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestCreateSnapshotsParametersAndRunnerGeneratesBothFormats(t *testing.T) {
	storage, libraryService, taskService, book := newGenerationTest(t)
	_ = storage
	base := txtconfig.Defaults()
	service := NewService(libraryService, taskService, base)
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
	if snapshot.Metadata.Title != "任务标题" || snapshot.Metadata.Author != "作者" || snapshot.TXT.SplitLevel != 1 || snapshot.Style.LineHeight != 1.8 || snapshot.ExpectedSHA256 != book.Original.SHA256 {
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
	if err != nil || len(kindle) != 1 || kindle[0].File.Format != "azw3" {
		t.Fatalf("Kindle projection = %#v, %v", kindle, err)
	}
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

func TestGenerationFailsWhenImmutableSourceHashChanges(t *testing.T) {
	_, libraryService, taskService, book := newGenerationTest(t)
	service := NewService(libraryService, taskService, txtconfig.Defaults())
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

func newGenerationTest(t *testing.T) (*store.Store, *library.Service, *task.Service, library.Book) {
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
	return storage, libraryService, task.NewService(storage), result.Book
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
