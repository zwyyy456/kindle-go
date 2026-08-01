package proofread

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestReviewProjectsAutomaticPendingConflictAndPagination(t *testing.T) {
	service, bookID, runID := newReviewService(t, []store.ProofreadCandidateRecord{
		reviewRecord("auto-a", 0, 4, "high", "high", "甲", "甲改"),
		reviewRecord("auto-b", 2, 5, "high", "high", "乙", "乙改"),
		reviewRecord("pending", 8, 10, "review", "review", "丙", "丙改"),
	})
	page, ok, err := service.Review(context.Background(), bookID, runID, ReviewQuery{PageSize: 2})
	if err != nil || !ok || page.Total != 3 || len(page.Candidates) != 2 || !page.HasNext {
		t.Fatalf("page = %#v, %v, %v", page, ok, err)
	}
	if page.Candidates[0].Outcome != "automatic" || len(page.Candidates[0].ConflictIDs) != 1 || page.Candidates[0].Context == "" {
		t.Fatalf("automatic candidate = %#v", page.Candidates[0])
	}
	pending, ok, err := service.Review(context.Background(), bookID, runID, ReviewQuery{Filter: "pending"})
	if err != nil || !ok || pending.Total != 1 || pending.Candidates[0].Candidate.ID != "pending" {
		t.Fatalf("pending page = %#v, %v, %v", pending, ok, err)
	}
}

func TestDecisionsAreAppendOnlyAndLatestDecisionWins(t *testing.T) {
	service, bookID, runID := newReviewService(t, []store.ProofreadCandidateRecord{
		reviewRecord("candidate", 0, 2, "review", "review", "错", "正"),
	})
	if _, err := service.Decide(context.Background(), "candidate", DecisionRequest{Decision: "modify", Replacement: "更正"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(context.Background(), "candidate", DecisionRequest{Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	page, ok, err := service.Review(context.Background(), bookID, runID, ReviewQuery{})
	if err != nil || !ok || len(page.Candidates) != 1 {
		t.Fatalf("page = %#v, %v, %v", page, ok, err)
	}
	view := page.Candidates[0]
	if view.Outcome != "rejected" || len(view.History) != 2 || view.History[0].Decision != "modify" || view.History[1].Decision != "reject" {
		t.Fatalf("decision projection = %#v", view)
	}
}

func TestConcurrentConflictingAcceptsCannotBothApply(t *testing.T) {
	service, _, runID := newReviewService(t, []store.ProofreadCandidateRecord{
		reviewRecord("left", 0, 4, "review", "review", "甲", "甲改"),
		reviewRecord("right", 2, 5, "review", "review", "乙", "乙改"),
	})
	start := make(chan struct{})
	errorsByCandidate := make(chan error, 2)
	var wait sync.WaitGroup
	for _, id := range []string{"left", "right"} {
		wait.Add(1)
		go func(candidateID string) {
			defer wait.Done()
			<-start
			_, err := service.Decide(context.Background(), candidateID, DecisionRequest{Decision: "accept"})
			errorsByCandidate <- err
		}(id)
	}
	close(start)
	wait.Wait()
	close(errorsByCandidate)
	var succeeded, failed int
	for err := range errorsByCandidate {
		if err == nil {
			succeeded++
		} else {
			failed++
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Fatalf("concurrent decisions succeeded=%d failed=%d", succeeded, failed)
	}
	decisions, err := service.store.CandidateDecisions(context.Background(), runID)
	if err != nil || len(decisions) != 1 {
		t.Fatalf("decisions = %#v, %v", decisions, err)
	}
}

func TestModifyRejectsInvalidOrImageReplacement(t *testing.T) {
	service, _, _ := newReviewService(t, []store.ProofreadCandidateRecord{
		reviewRecord("text", 0, 2, "review", "review", "错", "正"),
		{ID: "image", Kind: "image", LocationJSON: `{"image_usage_id":"usage-1"}`, ExpectedOriginal: "ad.png", Category: "external_ad_image", FirstConfidence: "review", FirstReplacement: "remove_reference", Verification: "review", VerifiedReplacement: "remove_reference"},
	})
	if _, err := service.Decide(context.Background(), "text", DecisionRequest{Decision: "modify", Replacement: "a\nb"}); err == nil {
		t.Fatal("multiline replacement unexpectedly accepted")
	}
	if _, err := service.Decide(context.Background(), "image", DecisionRequest{Decision: "modify", Replacement: "x"}); err == nil {
		t.Fatal("image text replacement unexpectedly accepted")
	}
}

func TestDecideReturnsStoreErrorInsteadOfUserError(t *testing.T) {
	service, _, _ := newReviewService(t, []store.ProofreadCandidateRecord{
		reviewRecord("candidate", 0, 2, "review", "review", "错", "正"),
	})
	if err := service.store.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := service.Decide(context.Background(), "candidate", DecisionRequest{Decision: "reject"})
	var userErr *UserError
	if err == nil || errors.As(err, &userErr) || !strings.Contains(err.Error(), "database is closed") {
		t.Fatalf("Decide store error = %v", err)
	}
}

func newReviewService(t *testing.T, candidates []store.ProofreadCandidateRecord) (*Service, string, string) {
	t.Helper()
	storage, libraryService, settingsService, taskService := newProofreadTest(t)
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("测试正文"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	created, err := taskService.Create(context.Background(), task.CreateRequest{BookID: book.ID, Type: task.Proofread, InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{string(task.Proofread)}, time.Now()); err != nil || !ok {
		t.Fatalf("claim task = %v, %v", ok, err)
	}
	runID := "run-review"
	workRel := filepath.ToSlash(filepath.Join("work", created.ID, "proofread-state"))
	workPath, err := storage.ResolveRel(workRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatal(err)
	}
	contextRecords := make([]map[string]any, 0, len(candidates))
	for index := range candidates {
		candidates[index].RunID = runID
		candidates[index].CreatedAt = time.Now().Add(time.Duration(index) * time.Millisecond)
		contextRecords = append(contextRecords, map[string]any{"candidate_id": candidates[index].ID, "context": "候选上下文 " + candidates[index].ID})
	}
	if err := writeJSONLines(filepath.Join(workPath, "candidates.jsonl"), contextRecords); err != nil {
		t.Fatal(err)
	}
	if err := storage.CommitProofreadRun(context.Background(), store.ProofreadRunRecord{
		ID: runID, BookID: book.ID, SourceFileID: book.Original.ID, TaskID: created.ID, SourceSHA256: book.Original.SHA256,
		Format: "txt", BatchSize: 12000, Concurrency: 3, EngineVersion: "test", CreatedAt: time.Now(),
	}, candidates, workRel, time.Now()); err != nil {
		t.Fatal(err)
	}
	return NewService(storage, libraryService, taskService, settingsService), book.ID, runID
}

func reviewRecord(id string, start, end int, first, verification, original, proposed string) store.ProofreadCandidateRecord {
	location, _ := json.Marshal(map[string]any{"line": 1, "start_char": start, "end_char": end})
	return store.ProofreadCandidateRecord{
		ID: id, Kind: "text", LocationJSON: string(location), ExpectedOriginal: original, Category: "wrong_character",
		FirstConfidence: first, FirstReplacement: proposed, Verification: verification, VerifiedReplacement: proposed, Reason: "test",
	}
}
