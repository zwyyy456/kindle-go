package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommitRevisionDoesNotExposeAnyFileWhenCancellationWins(t *testing.T) {
	storage, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("正文"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	taskRecord, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "build_revision_txt", InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{"build_revision_txt"}, time.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	workRel := filepath.ToSlash(filepath.Join("work", taskRecord.ID, "revision-output"))
	workPath, err := storage.ResolveRel(workRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"revision.txt": "修订", "report.md": "报告", "audit.jsonl": "{}\n"} {
		if err := os.WriteFile(filepath.Join(workPath, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, changed, err := storage.CancelTask(context.Background(), taskRecord.ID, time.Now()); err != nil || !changed {
		t.Fatalf("cancel = %v, %v", changed, err)
	}
	_, err = storage.CommitRevision(context.Background(), RevisionCommit{
		BookID: book.ID, SourceFileID: book.Original.ID, TaskID: taskRecord.ID, ProofreadRunID: "",
		Format: "txt", RevisionName: "revision.txt", ReportName: "report.md", AuditName: "audit.jsonl", WorkDirRel: workRel,
	})
	if err == nil {
		t.Fatal("canceled revision commit unexpectedly succeeded")
	}
	files, err := storage.FilesForBook(context.Background(), book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Role != "original" {
		t.Fatalf("canceled revision exposed files = %#v", files)
	}
}
