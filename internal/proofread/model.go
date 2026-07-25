package proofread

import (
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

type Run struct {
	ID            string
	BookID        string
	SourceFileID  string
	TaskID        string
	SourceSHA256  string
	Format        string
	Model         string
	Status        string
	EngineVersion string
	BatchSize     int
	Concurrency   int
	CreatedAt     time.Time
	CompletedAt   time.Time
}

type Candidate struct {
	ID                  string
	RunID               string
	Kind                string
	LocationJSON        string
	ExpectedOriginal    string
	Category            string
	FirstConfidence     string
	FirstReplacement    string
	Verification        string
	VerifiedReplacement string
	Reason              string
	CreatedAt           time.Time
}

type Decision struct {
	ID          string
	CandidateID string
	Decision    string
	Replacement string
	CreatedAt   time.Time
}

type ReviewQuery struct {
	Filter   string
	Page     int
	PageSize int
}

type CandidateView struct {
	Candidate   Candidate
	Context     string
	Location    string
	Outcome     string
	Replacement string
	Automatic   bool
	ConflictIDs []string
	History     []Decision
}

type ReviewPage struct {
	Run         Run
	Candidates  []CandidateView
	Filter      string
	Page        int
	Total       int
	HasPrevious bool
	HasNext     bool
	Unresolved  int
}

type DecisionRequest struct {
	Decision    string
	Replacement string
}

func runFromStore(value store.ProofreadRunRecord) Run {
	return Run{
		ID: value.ID, BookID: value.BookID, SourceFileID: value.SourceFileID, TaskID: value.TaskID,
		SourceSHA256: value.SourceSHA256, Format: value.Format, Model: value.Model, Status: value.Status,
		EngineVersion: value.EngineVersion, BatchSize: value.BatchSize, Concurrency: value.Concurrency,
		CreatedAt: value.CreatedAt, CompletedAt: value.CompletedAt,
	}
}

func candidateFromStore(value store.ProofreadCandidateRecord) Candidate {
	return Candidate{
		ID: value.ID, RunID: value.RunID, Kind: value.Kind, LocationJSON: value.LocationJSON,
		ExpectedOriginal: value.ExpectedOriginal, Category: value.Category,
		FirstConfidence: value.FirstConfidence, FirstReplacement: value.FirstReplacement,
		Verification: value.Verification, VerifiedReplacement: value.VerifiedReplacement,
		Reason: value.Reason, CreatedAt: value.CreatedAt,
	}
}

func decisionFromStore(value store.CandidateDecisionRecord) Decision {
	return Decision{
		ID: value.ID, CandidateID: value.CandidateID, Decision: value.Decision,
		Replacement: value.Replacement, CreatedAt: value.CreatedAt,
	}
}
