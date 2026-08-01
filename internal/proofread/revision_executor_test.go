package proofread

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestRevisionExecutorCreatesImmutableTXTReportAuditAndDecisionSnapshots(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n他对此噗之以鼻。\n")})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage, libraryService, taskService, settingsService)
	proofreadTask, err := service.Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, &fakeProofreadModel{proposeTXT: true}), proofreadTask.ID, task.Completed)
	runs, err := service.Runs(context.Background(), imported.Book.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	firstTask, err := service.CreateRevision(context.Background(), runs[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	runRevisionTask(t, taskService, NewRevisionExecutor(storage, libraryService, nil), firstTask.ID, task.Completed)
	firstFiles := revisionFilesForTask(t, libraryService, imported.Book.ID, firstTask.ID)
	firstRevision := fileByRole(t, firstFiles, "revision")
	firstBytes := downloadBytes(t, libraryService, firstRevision.ID)
	if !strings.Contains(string(firstBytes), "嗤之以鼻") || len(fileByRole(t, firstFiles, "report").ID) == 0 || len(fileByRole(t, firstFiles, "audit").ID) == 0 {
		t.Fatalf("first revision files = %#v, body = %q", firstFiles, firstBytes)
	}
	firstSnapshot, err := storage.RevisionCandidates(context.Background(), firstRevision.ID)
	if err != nil || len(firstSnapshot) != 1 || firstSnapshot[0].Outcome != "apply_automatic" {
		t.Fatalf("first snapshot = %#v, %v", firstSnapshot, err)
	}
	repeatedTask, err := service.CreateRevision(context.Background(), runs[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := storage.ProofreadCandidates(context.Background(), runs[0].ID)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
	if _, err := service.Decide(context.Background(), candidates[0].ID, DecisionRequest{Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	runRevisionTask(t, taskService, NewRevisionExecutor(storage, libraryService, nil), repeatedTask.ID, task.Completed)
	repeatedRevision := fileByRole(t, revisionFilesForTask(t, libraryService, imported.Book.ID, repeatedTask.ID), "revision")
	if !bytes.Equal(firstBytes, downloadBytes(t, libraryService, repeatedRevision.ID)) {
		t.Fatal("the queued frozen snapshot changed after a later user decision")
	}
	secondTask, err := service.CreateRevision(context.Background(), runs[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	runRevisionTask(t, taskService, NewRevisionExecutor(storage, libraryService, nil), secondTask.ID, task.Completed)
	secondFiles := revisionFilesForTask(t, libraryService, imported.Book.ID, secondTask.ID)
	secondRevision := fileByRole(t, secondFiles, "revision")
	secondBytes := downloadBytes(t, libraryService, secondRevision.ID)
	if !strings.Contains(string(secondBytes), "噗之以鼻") || strings.Contains(string(secondBytes), "嗤之以鼻") {
		t.Fatalf("second revision body = %q", secondBytes)
	}
	if !bytes.Equal(firstBytes, downloadBytes(t, libraryService, firstRevision.ID)) {
		t.Fatal("later decisions changed the first immutable revision")
	}
	secondSnapshot, err := storage.RevisionCandidates(context.Background(), secondRevision.ID)
	if err != nil || len(secondSnapshot) != 1 || secondSnapshot[0].Outcome != "keep_rejected" {
		t.Fatalf("second snapshot = %#v, %v", secondSnapshot, err)
	}
}

func TestRevisionRequiresConfirmationForUnresolvedAndKeepsOriginal(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n他对此噗之以鼻。\n")})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage, libraryService, taskService, settingsService)
	proofreadTask, err := service.Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeProofreadModel{proposeTXT: true, verificationVerdict: "review"}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, model), proofreadTask.ID, task.Completed)
	runs, _ := service.Runs(context.Background(), imported.Book.ID)
	if _, err := service.CreateRevision(context.Background(), runs[0].ID, false); err == nil {
		t.Fatal("unresolved revision unexpectedly skipped confirmation")
	}
	created, err := service.CreateRevision(context.Background(), runs[0].ID, true)
	if err != nil {
		t.Fatal(err)
	}
	runRevisionTask(t, taskService, NewRevisionExecutor(storage, libraryService, nil), created.ID, task.Completed)
	file := fileByRole(t, revisionFilesForTask(t, libraryService, imported.Book.ID, created.ID), "revision")
	if !file.HasUnresolved || !strings.Contains(string(downloadBytes(t, libraryService, file.ID)), "噗之以鼻") {
		t.Fatalf("unresolved revision = %#v", file)
	}
}

func TestRevisionFailsIfImmutableSourceHashChanges(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n正文\n")})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage, libraryService, taskService, settingsService)
	proofreadTask, _ := service.Create(context.Background(), imported.Book.ID)
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, &fakeProofreadModel{}), proofreadTask.ID, task.Completed)
	runs, _ := service.Runs(context.Background(), imported.Book.ID)
	revisionTask, err := service.CreateRevision(context.Background(), runs[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	storedBook, _, err := storage.Book(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, err := storage.ResolveRel(storedBook.Original.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	runRevisionTask(t, taskService, NewRevisionExecutor(storage, libraryService, nil), revisionTask.ID, task.Failed)
	failed, _, _ := taskService.Get(context.Background(), revisionTask.ID)
	if failed.ErrorCode != "source_hash_mismatch" {
		t.Fatalf("failed revision = %#v", failed)
	}
	if files := revisionFilesForTask(t, libraryService, imported.Book.ID, revisionTask.ID); len(files) != 0 {
		t.Fatalf("failed revision exposed files = %#v", files)
	}
}

func TestRevisionMarksMissingSourceAsUnavailable(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n正文\n")})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage, libraryService, taskService, settingsService)
	proofreadTask, err := service.Create(context.Background(), imported.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, &fakeProofreadModel{}), proofreadTask.ID, task.Completed)
	runs, err := service.Runs(context.Background(), imported.Book.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	revisionTask, err := service.CreateRevision(context.Background(), runs[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath, _, err := libraryService.ResolveOriginal(context.Background(), imported.Book.ID, imported.Book.Original.ID, imported.Book.Original.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}

	runRevisionTask(t, taskService, NewRevisionExecutor(storage, libraryService, nil), revisionTask.ID, task.Failed)
	failed, _, err := taskService.Get(context.Background(), revisionTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.ErrorCode != "source_unavailable" {
		t.Fatalf("failed revision = %#v", failed)
	}
}

func TestRevisionExecutorPreservesMinimalEPUBAndStoresCompatibility(t *testing.T) {
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	source := minimalProofreadEPUB(t)
	imported, err := libraryService.Import(context.Background(), library.ImportRequest{Filename: "book.epub", Reader: bytes.NewReader(source)})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage, libraryService, taskService, settingsService)
	proofreadTask, _ := service.Create(context.Background(), imported.Book.ID)
	runProofreadTask(t, taskService, NewExecutor(storage, libraryService, nil, &fakeProofreadModel{}), proofreadTask.ID, task.Completed)
	runs, _ := service.Runs(context.Background(), imported.Book.ID)
	revisionTask, err := service.CreateRevision(context.Background(), runs[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	runRevisionTask(t, taskService, NewRevisionExecutor(storage, libraryService, nil), revisionTask.ID, task.Completed)
	revision := fileByRole(t, revisionFilesForTask(t, libraryService, imported.Book.ID, revisionTask.ID), "revision")
	assertEPUBEntriesPreserved(t, source, downloadBytes(t, libraryService, revision.ID))
	report, ok, err := libraryService.CompatibilityForFile(context.Background(), revision.ID)
	if err != nil || !ok || report.Status != "passed" || report.SourceSHA256 != revision.SHA256 {
		t.Fatalf("revision compatibility = %#v, %v, %v", report, ok, err)
	}
}

func TestValidateRevisionSnapshotRejectsOverlappingApplyResults(t *testing.T) {
	candidates := []store.ProofreadCandidateRecord{
		reviewRecord("left", 0, 4, "high", "high", "a", "b"),
		reviewRecord("right", 2, 5, "high", "high", "c", "d"),
	}
	decisions := make([]RevisionDecision, 0, len(candidates))
	for _, candidate := range candidates {
		decisions = append(decisions, RevisionDecision{CandidateID: candidate.ID, Outcome: "apply_automatic", Replacement: candidate.FirstReplacement, ExpectedOriginal: candidate.ExpectedOriginal, LocationJSON: candidate.LocationJSON})
	}
	if err := validateRevisionSnapshot(candidates, decisions); err == nil {
		t.Fatal("overlapping revision snapshot unexpectedly validated")
	}
}

func runRevisionTask(t *testing.T, taskService *task.Service, executor *RevisionExecutor, id string, want task.Status) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- task.NewRunner(taskService, map[task.Type]task.Executor{task.BuildRevisionTXT: executor, task.BuildRevisionEPUB: executor}).Run(ctx)
	}()
	deadline := time.Now().Add(20 * time.Second)
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
	t.Fatalf("revision task = %#v, want %s", value, want)
}

func revisionFilesForTask(t *testing.T, libraryService *library.Service, bookID, taskID string) []library.File {
	t.Helper()
	detail, ok, err := libraryService.GetBookDetail(context.Background(), bookID)
	if err != nil || !ok {
		t.Fatalf("book detail = %#v, %v, %v", detail, ok, err)
	}
	var result []library.File
	for _, file := range detail.Files {
		if file.TaskID == taskID {
			result = append(result, file)
		}
	}
	return result
}

func fileByRole(t *testing.T, files []library.File, role string) library.File {
	t.Helper()
	for _, file := range files {
		if file.Role == role {
			return file
		}
	}
	t.Fatalf("role %q not found in %#v", role, files)
	return library.File{}
}

func downloadBytes(t *testing.T, libraryService *library.Service, fileID string) []byte {
	t.Helper()
	path, _, err := libraryService.DownloadFile(context.Background(), fileID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertEPUBEntriesPreserved(t *testing.T, source, revised []byte) {
	t.Helper()
	before, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	after, err := zip.NewReader(bytes.NewReader(revised), int64(len(revised)))
	if err != nil {
		t.Fatal(err)
	}
	if len(before.File) != len(after.File) {
		t.Fatalf("EPUB entry count changed: %d != %d", len(before.File), len(after.File))
	}
	for index := range before.File {
		left, right := before.File[index], after.File[index]
		if left.Name != right.Name || left.Method != right.Method || left.ExternalAttrs != right.ExternalAttrs || !bytes.Equal(left.Extra, right.Extra) || left.Comment != right.Comment {
			t.Fatalf("EPUB metadata changed for %q", left.Name)
		}
		leftReader, err := left.Open()
		if err != nil {
			t.Fatal(err)
		}
		leftData, leftErr := io.ReadAll(leftReader)
		_ = leftReader.Close()
		rightReader, err := right.Open()
		if err != nil {
			t.Fatal(err)
		}
		rightData, rightErr := io.ReadAll(rightReader)
		_ = rightReader.Close()
		if leftErr != nil || rightErr != nil || !bytes.Equal(leftData, rightData) {
			t.Fatalf("EPUB entry content changed for %q: %v, %v", left.Name, leftErr, rightErr)
		}
	}
}
