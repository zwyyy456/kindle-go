package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommitProofreadRunDoesNotExposeCanceledTaskResults(t *testing.T) {
	storage, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("正文"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	created, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "proofread", InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{"proofread"}, time.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	workRel := filepath.ToSlash(filepath.Join("work", created.ID, "proofread-state"))
	workPath, err := storage.ResolveRel(workRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := storage.CancelTask(context.Background(), created.ID, time.Now()); err != nil || !changed {
		t.Fatalf("cancel = %v, %v", changed, err)
	}
	runID, _ := NewID()
	err = storage.CommitProofreadRun(context.Background(), ProofreadRunRecord{ID: runID, BookID: book.ID, SourceFileID: book.Original.ID, TaskID: created.ID, SourceSHA256: book.Original.SHA256, Format: "txt", BatchSize: 12000, Concurrency: 3, EngineVersion: "test", CreatedAt: time.Now()}, nil, workRel, time.Now())
	if err == nil {
		t.Fatal("canceled proofread commit unexpectedly succeeded")
	}
	runs, err := storage.ProofreadRuns(context.Background(), book.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	if _, err := os.Stat(workPath); err != nil {
		t.Fatalf("work state was not restored: %v", err)
	}
}
