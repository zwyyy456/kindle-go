//go:build realcodex

package proofread

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

const realCodexAcceptanceEnv = "KINDLE_GO_CONFIRM_REAL_CODEX"

func TestRealCodexTXTAndEPUBAcceptance(t *testing.T) {
	if os.Getenv(realCodexAcceptanceEnv) != "1" {
		t.Skip("set " + realCodexAcceptanceEnv + "=1 to allow real Codex model calls on synthetic fixtures")
	}
	status, err := CheckCodexLogin(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("Codex login check: %v", err)
	}
	t.Logf("Codex login: %s", status)

	tests := []struct {
		name, filename string
		content        func(*testing.T) []byte
	}{
		{
			name: "TXT", filename: "synthetic.txt",
			content: func(*testing.T) []byte { return []byte("第一章\n今天阳光很好。\n") },
		},
		{
			name: "EPUB", filename: "synthetic.epub",
			content: minimalProofreadEPUB,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage, libraryService, settingsService, taskService := newProofreadTest(t)
			values, err := settingsService.Current(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			values.Proofread.BatchSize = 1000
			values.Proofread.Concurrency = 1
			if err := settingsService.Save(context.Background(), values); err != nil {
				t.Fatal(err)
			}
			content := test.content(t)
			imported, err := libraryService.Import(context.Background(), library.ImportRequest{
				Filename: test.filename,
				Reader:   bytes.NewReader(content),
			})
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(storage, libraryService, taskService, settingsService)
			proofreadTask, err := service.Create(context.Background(), imported.Book.ID)
			if err != nil {
				t.Fatal(err)
			}
			runRealAcceptanceTask(t, taskService, map[task.Type]task.Executor{
				task.Proofread: NewExecutor(storage, libraryService, nil, nil),
			}, proofreadTask.ID, 30*time.Minute)

			runs, err := service.Runs(context.Background(), imported.Book.ID)
			if err != nil || len(runs) != 1 || runs[0].Status != "completed" {
				t.Fatalf("proofread runs = %#v, %v", runs, err)
			}
			revisionTask, err := service.CreateRevision(context.Background(), runs[0].ID, true)
			if err != nil {
				t.Fatal(err)
			}
			revisionExecutor := NewRevisionExecutor(storage, libraryService, nil)
			runRealAcceptanceTask(t, taskService, map[task.Type]task.Executor{
				task.BuildRevisionTXT:  revisionExecutor,
				task.BuildRevisionEPUB: revisionExecutor,
			}, revisionTask.ID, 2*time.Minute)

			files := revisionFilesForTask(t, libraryService, imported.Book.ID, revisionTask.ID)
			if len(files) != 3 {
				t.Fatalf("revision deliverables = %#v", files)
			}
			for _, role := range []string{"revision", "report", "audit"} {
				file := fileByRole(t, files, role)
				if file.ID == "" || strings.TrimSpace(file.SHA256) == "" {
					t.Fatalf("%s deliverable = %#v", role, file)
				}
				data := downloadBytes(t, libraryService, file.ID)
				if int64(len(data)) != file.Size {
					t.Fatalf("%s downloaded size = %d, want %d", role, len(data), file.Size)
				}
				if role != "audit" && file.Size == 0 {
					t.Fatalf("%s deliverable is empty", role)
				}
			}
			if imported.Book.SourceFormat == "epub" {
				revision := fileByRole(t, files, "revision")
				report, ok, err := libraryService.CompatibilityForFile(context.Background(), revision.ID)
				if err != nil || !ok || report.Status != "passed" || report.SourceSHA256 != revision.SHA256 {
					t.Fatalf("revised EPUB compatibility = %#v, %v, %v", report, ok, err)
				}
			}
			t.Logf("completed %s proofread run %s and revision task %s", test.name, runs[0].ID, revisionTask.ID)
		})
	}
}

func runRealAcceptanceTask(t *testing.T, taskService *task.Service, executors map[task.Type]task.Executor, taskID string, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- task.NewRunner(taskService, executors).Run(ctx) }()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		value, ok, err := taskService.Get(context.Background(), taskID)
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if ok && (value.Status == task.Completed || value.Status == task.Failed || value.Status == task.Canceled) {
			cancel()
			if runnerErr := <-done; runnerErr != nil {
				t.Fatalf("runner: %v", runnerErr)
			}
			if value.Status != task.Completed {
				events, _ := taskService.Events(context.Background(), taskID)
				t.Fatalf("task ended as %s: %s (%s); events=%#v", value.Status, value.ErrorMessage, value.ErrorCode, events)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	<-done
	value, _, _ := taskService.Get(context.Background(), taskID)
	t.Fatalf("task timed out after %s: %#v", timeout, value)
}
