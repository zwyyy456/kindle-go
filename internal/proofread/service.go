package proofread

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
	epubengine "github.com/flashdict/kindle2flashdict/long-epub-proofreader"
	novelengine "github.com/flashdict/kindle2flashdict/long-novel-proofreader"
)

type Parameters struct {
	SchemaVersion  int    `json:"version"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Format         string `json:"format"`
	Model          string `json:"model"`
	BatchSize      int    `json:"batch_size"`
	Concurrency    int    `json:"concurrency"`
	EngineVersion  string `json:"engine_version"`
}

type UserError struct {
	err error
}

func (e *UserError) Error() string { return e.err.Error() }

func (e *UserError) Unwrap() error { return e.err }

func userErrorf(format string, args ...any) error {
	return &UserError{err: fmt.Errorf(format, args...)}
}

type Service struct {
	store    *store.Store
	library  *library.Service
	tasks    *task.Service
	settings *settings.Service
	now      func() time.Time
	decision sync.Mutex
}

func NewService(storage *store.Store, libraryService *library.Service, taskService *task.Service, settingsService *settings.Service) *Service {
	return &Service{store: storage, library: libraryService, tasks: taskService, settings: settingsService, now: time.Now}
}

func (s *Service) Create(ctx context.Context, bookID string) (task.Task, error) {
	book, ok, err := s.library.GetBook(ctx, bookID)
	if err != nil {
		return task.Task{}, err
	}
	if !ok || (book.SourceFormat != "txt" && book.SourceFormat != "epub") {
		return task.Task{}, userErrorf("proofreading is only available for TXT and EPUB books")
	}
	existingTasks, err := s.tasks.List(ctx, bookID)
	if err != nil {
		return task.Task{}, err
	}
	for _, existing := range existingTasks {
		if existing.Type == task.Proofread && (existing.Status == task.Queued || existing.Status == task.Running) {
			return task.Task{}, userErrorf("book already has an active proofreading task")
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
		SchemaVersion:  taskParametersVersion,
		ExpectedSHA256: book.Original.SHA256, Format: book.SourceFormat, Model: values.Proofread.Model,
		BatchSize: values.Proofread.BatchSize, Concurrency: values.Proofread.Concurrency, EngineVersion: version,
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return task.Task{}, err
	}
	return s.tasks.Create(ctx, task.CreateRequest{BookID: book.ID, Type: task.Proofread, InputFileID: book.Original.ID, ParametersJSON: string(encoded), CreatedAt: s.now()})
}

func (s *Service) Runs(ctx context.Context, bookID string) ([]Run, error) {
	records, err := s.store.ProofreadRuns(ctx, bookID)
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(records))
	for _, record := range records {
		runs = append(runs, runFromStore(record))
	}
	return runs, nil
}

func (s *Service) Run(ctx context.Context, runID string) (Run, bool, error) {
	record, ok, err := s.store.ProofreadRun(ctx, runID)
	if err != nil || !ok {
		return Run{}, ok, err
	}
	return runFromStore(record), true, nil
}

func (s *Service) Review(ctx context.Context, bookID, runID string, query ReviewQuery) (ReviewPage, bool, error) {
	run, ok, err := s.store.ProofreadRun(ctx, runID)
	if err != nil || !ok || run.BookID != bookID {
		return ReviewPage{}, false, err
	}
	views, err := s.candidateViews(ctx, run)
	if err != nil {
		return ReviewPage{}, false, err
	}
	unresolved := 0
	for _, view := range views {
		if view.Outcome == "pending" || len(view.ConflictIDs) != 0 {
			unresolved++
		}
	}
	filter := strings.TrimSpace(query.Filter)
	if filter != "" && filter != "all" {
		filtered := views[:0]
		for _, view := range views {
			if filter == view.Outcome || (filter == "conflict" && len(view.ConflictIDs) != 0) {
				filtered = append(filtered, view)
			}
		}
		views = filtered
	}
	pageSize := query.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	page := query.Page
	if page < 1 {
		page = 1
	}
	total := len(views)
	start := min((page-1)*pageSize, total)
	end := min(start+pageSize, total)
	return ReviewPage{
		Run: runFromStore(run), Candidates: views[start:end], Filter: filter, Page: page, Total: total,
		HasPrevious: page > 1, HasNext: end < total, Unresolved: unresolved,
	}, true, nil
}

type RevisionDecision struct {
	CandidateID      string `json:"candidate_id"`
	Outcome          string `json:"outcome"`
	Replacement      string `json:"replacement"`
	ExpectedOriginal string `json:"expected_original"`
	LocationJSON     string `json:"location_json"`
}

type RevisionParameters struct {
	SchemaVersion int                `json:"version"`
	RunID         string             `json:"run_id"`
	SourceFileID  string             `json:"source_file_id"`
	SourceSHA256  string             `json:"source_sha256"`
	Format        string             `json:"format"`
	EngineVersion string             `json:"engine_version"`
	HasUnresolved bool               `json:"has_unresolved"`
	Decisions     []RevisionDecision `json:"decisions"`
}

func (s *Service) CreateRevision(ctx context.Context, runID string, confirmUnresolved bool) (task.Task, error) {
	run, ok, err := s.store.ProofreadRun(ctx, runID)
	if err != nil {
		return task.Task{}, err
	}
	if !ok || run.Status != "completed" {
		return task.Task{}, userErrorf("completed proofread run not found")
	}
	views, err := s.candidateViews(ctx, run)
	if err != nil {
		return task.Task{}, err
	}
	parameters := RevisionParameters{
		SchemaVersion: taskParametersVersion,
		RunID:         run.ID, SourceFileID: run.SourceFileID, SourceSHA256: run.SourceSHA256, Format: run.Format, EngineVersion: run.EngineVersion,
		Decisions: make([]RevisionDecision, 0, len(views)),
	}
	for _, view := range views {
		outcome := "keep_pending"
		replacement := ""
		switch {
		case len(view.ConflictIDs) != 0:
			outcome = "keep_conflict"
			parameters.HasUnresolved = true
		case view.Outcome == "automatic":
			outcome, replacement = "apply_automatic", view.Replacement
		case view.Outcome == "accepted":
			outcome, replacement = "apply_accepted", view.Replacement
		case view.Outcome == "modified":
			outcome, replacement = "apply_modified", view.Replacement
		case view.Outcome == "rejected":
			outcome = "keep_rejected"
		default:
			parameters.HasUnresolved = true
		}
		parameters.Decisions = append(parameters.Decisions, RevisionDecision{
			CandidateID: view.Candidate.ID, Outcome: outcome, Replacement: replacement,
			ExpectedOriginal: view.Candidate.ExpectedOriginal, LocationJSON: view.Candidate.LocationJSON,
		})
	}
	if parameters.HasUnresolved && !confirmUnresolved {
		return task.Task{}, userErrorf("unresolved candidates require explicit confirmation; their source text will be kept")
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return task.Task{}, err
	}
	taskType := task.BuildRevisionTXT
	if run.Format == "epub" {
		taskType = task.BuildRevisionEPUB
	}
	return s.tasks.Create(ctx, task.CreateRequest{
		BookID: run.BookID, Type: taskType, InputFileID: run.SourceFileID, ParametersJSON: string(encoded), CreatedAt: s.now(),
	})
}

func (s *Service) Decide(ctx context.Context, candidateID string, request DecisionRequest) (Run, error) {
	s.decision.Lock()
	defer s.decision.Unlock()
	candidate, ok, err := s.store.ProofreadCandidate(ctx, candidateID)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, userErrorf("candidate not found")
	}
	run, ok, err := s.store.ProofreadRun(ctx, candidate.RunID)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, userErrorf("proofread run not found")
	}
	result := runFromStore(run)
	request.Decision = strings.TrimSpace(request.Decision)
	request.Replacement = strings.TrimSpace(request.Replacement)
	switch request.Decision {
	case "accept":
		request.Replacement = candidate.FirstReplacement
	case "reject":
		request.Replacement = ""
	case "modify":
		if candidate.Kind != "text" {
			return result, userErrorf("image candidates cannot use a text replacement")
		}
		if request.Replacement == "" {
			return result, userErrorf("modified replacement must not be empty")
		}
		if strings.ContainsAny(request.Replacement, "\r\n") {
			return result, userErrorf("modified replacement must stay on one line")
		}
	default:
		return result, userErrorf("decision must be accept, reject, or modify")
	}
	if request.Decision != "reject" {
		views, err := s.candidateViews(ctx, run)
		if err != nil {
			return result, err
		}
		for index := range views {
			if views[index].Candidate.ID == candidateID {
				views[index].Outcome = request.Decision + "ed"
				if request.Decision == "modify" {
					views[index].Outcome = "modified"
				}
				views[index].Replacement = request.Replacement
				views[index].ConflictIDs = nil
			}
		}
		markConflicts(views)
		for _, view := range views {
			if view.Candidate.ID == candidateID && len(view.ConflictIDs) != 0 {
				return result, userErrorf("candidate_conflict: candidate overlaps another applied candidate; reject the conflicting candidate first")
			}
		}
	}
	_, err = s.store.AppendCandidateDecision(ctx, candidateID, request.Decision, request.Replacement, s.now())
	return result, err
}

func (s *Service) candidateViews(ctx context.Context, run store.ProofreadRunRecord) ([]CandidateView, error) {
	candidates, err := s.store.ProofreadCandidates(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	decisions, err := s.store.CandidateDecisions(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	history := make(map[string][]Decision)
	for _, decision := range decisions {
		history[decision.CandidateID] = append(history[decision.CandidateID], decisionFromStore(decision))
	}
	contexts, err := s.candidateContexts(run)
	if err != nil {
		return nil, err
	}
	views := make([]CandidateView, 0, len(candidates))
	for _, candidate := range candidates {
		automatic := candidate.FirstConfidence == "high" && candidate.Verification == "high" && candidate.FirstReplacement == candidate.VerifiedReplacement
		view := CandidateView{Candidate: candidateFromStore(candidate), Context: contexts[candidate.ID], Location: locationLabel(candidate.LocationJSON), Automatic: automatic, History: history[candidate.ID]}
		if len(view.History) == 0 {
			if automatic {
				view.Outcome = "automatic"
				view.Replacement = candidate.FirstReplacement
			} else {
				view.Outcome = "pending"
			}
		} else {
			latest := view.History[len(view.History)-1]
			view.Outcome = map[string]string{"accept": "accepted", "reject": "rejected", "modify": "modified"}[latest.Decision]
			view.Replacement = latest.Replacement
		}
		views = append(views, view)
	}
	markConflicts(views)
	return views, nil
}

func (s *Service) candidateContexts(run store.ProofreadRunRecord) (map[string]string, error) {
	statePath, err := s.store.ResolveRel(run.EngineStateRelPath)
	if err != nil {
		return nil, err
	}
	records, err := readJSONMaps(filepath.Join(statePath, "candidates.jsonl"))
	if err != nil {
		return nil, err
	}
	contexts := make(map[string]string, len(records))
	for _, record := range records {
		contextValue := stringValue(record, "context")
		if contextValue == "" {
			contextValue = stringValue(record, "document_href")
		}
		contexts[stringValue(record, "candidate_id")] = contextValue
	}
	return contexts, nil
}

func markConflicts(views []CandidateView) {
	for index := range views {
		views[index].ConflictIDs = nil
	}
	for left := 0; left < len(views); left++ {
		if !applies(views[left].Outcome) {
			continue
		}
		for right := left + 1; right < len(views); right++ {
			if applies(views[right].Outcome) && candidatesConflict(views[left].Candidate, views[right].Candidate) {
				views[left].ConflictIDs = append(views[left].ConflictIDs, views[right].Candidate.ID)
				views[right].ConflictIDs = append(views[right].ConflictIDs, views[left].Candidate.ID)
			}
		}
	}
	for index := range views {
		sort.Strings(views[index].ConflictIDs)
	}
}

func applies(outcome string) bool {
	return outcome == "automatic" || outcome == "accepted" || outcome == "modified"
}

func candidatesConflict(left, right Candidate) bool {
	var a, b map[string]any
	if json.Unmarshal([]byte(left.LocationJSON), &a) != nil || json.Unmarshal([]byte(right.LocationJSON), &b) != nil {
		return false
	}
	if left.Kind == "image" && right.Kind == "image" {
		return stringValue(a, "image_usage_id") != "" && stringValue(a, "image_usage_id") == stringValue(b, "image_usage_id")
	}
	if left.Kind != "text" || right.Kind != "text" {
		return false
	}
	if block := stringValue(a, "block_id"); block != "" && block != stringValue(b, "block_id") {
		return false
	}
	startKey, endKey := "start_char", "end_char"
	if a[startKey] == nil {
		startKey, endKey = "start_offset", "end_offset"
	}
	leftStart, leftEnd := intValue(a[startKey]), intValue(a[endKey])
	rightStart, rightEnd := intValue(b[startKey]), intValue(b[endKey])
	return leftStart < rightEnd && rightStart < leftEnd
}

func intValue(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	default:
		return 0
	}
}

func locationLabel(encoded string) string {
	var location map[string]any
	if json.Unmarshal([]byte(encoded), &location) != nil {
		return encoded
	}
	if line := intValue(location["line"]); line > 0 {
		return fmt.Sprintf("line %d, characters %d–%d", line, intValue(location["start_char"]), intValue(location["end_char"]))
	}
	if usage := stringValue(location, "image_usage_id"); usage != "" {
		return fmt.Sprintf("image usage %s in %s", usage, stringValue(location, "document_href"))
	}
	return fmt.Sprintf("block %s, offsets %d–%d", stringValue(location, "block_id"), intValue(location["start_offset"]), intValue(location["end_offset"]))
}
