package proofread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/enginebundle"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
	epubengine "github.com/flashdict/kindle2flashdict/long-epub-proofreader"
	novelengine "github.com/flashdict/kindle2flashdict/long-novel-proofreader"
)

type ModelClient interface {
	Invoke(context.Context, CodexRequest, any) error
}

type Executor struct {
	store   *store.Store
	library *library.Service
	runner  CommandRunner
	model   ModelClient
	now     func() time.Time
}

func NewExecutor(storage *store.Store, libraryService *library.Service, runner CommandRunner, model ModelClient) *Executor {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return &Executor{store: storage, library: libraryService, runner: runner, model: model, now: time.Now}
}

type reviewCandidate struct {
	BatchID    string `json:"batch_id"`
	BlockID    string `json:"block_id,omitempty"`
	Line       int    `json:"line,omitempty"`
	Occurrence int    `json:"occurrence"`
	Original   string `json:"original"`
	Proposed   string `json:"proposed"`
	Category   string `json:"category"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
	Context    string `json:"context"`
}

type glossaryProposal struct {
	Term    string `json:"term"`
	Type    string `json:"type"`
	BatchID string `json:"batch_id"`
}

type firstReviewOutput struct {
	Candidates []reviewCandidate  `json:"candidates"`
	Glossary   []glossaryProposal `json:"glossary"`
}

type imageCandidate struct {
	ImageUsageID string `json:"image_usage_id"`
	Category     string `json:"category"`
	Confidence   string `json:"confidence"`
	Proposed     string `json:"proposed"`
	Reason       string `json:"reason"`
}

type imageReviewOutput struct {
	Candidates []imageCandidate `json:"candidates"`
}

type verificationOutput struct {
	CandidateID string `json:"candidate_id"`
	Verdict     string `json:"verdict"`
	Proposed    string `json:"proposed"`
	Reason      string `json:"reason"`
	ReviewMode  string `json:"review_mode"`
	Reviewer    string `json:"reviewer"`
}

type batchRecord struct {
	BatchID string `json:"batch_id"`
	Status  string `json:"status"`
}

type imageRecord struct {
	ImageID string `json:"image_id"`
	Status  string `json:"status"`
}

func (e *Executor) Execute(ctx context.Context, value task.Task, progress task.ProgressReporter) error {
	var params Parameters
	if err := json.Unmarshal([]byte(value.ParametersJSON), &params); err != nil {
		return &task.ExecutionError{Code: "proofread_engine_failed", Message: "invalid proofreading task parameters", Err: err}
	}
	if params.Format != "txt" && params.Format != "epub" {
		return &task.ExecutionError{Code: "proofread_engine_failed", Message: "unsupported proofreading format"}
	}
	if params.BatchSize < 1000 || params.BatchSize > 50000 || params.Concurrency < 1 || params.Concurrency > 8 {
		return &task.ExecutionError{Code: "proofread_engine_failed", Message: "invalid proofreading batch or concurrency settings"}
	}
	model := e.model
	if model == nil {
		model = CodexClient{Model: params.Model}
	}
	sourcePath, source, err := e.library.ResolveOriginal(ctx, value.BookID, value.InputFileID, params.ExpectedSHA256)
	if err != nil {
		return &task.ExecutionError{Code: "source_hash_mismatch", Err: err}
	}
	workRel := filepath.ToSlash(filepath.Join("work", value.ID))
	workDir, err := e.store.ResolveRel(workRel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	defer e.library.RemoveTaskWork(value.ID)
	runtime, err := e.releaseEngine(params.Format, filepath.Join(workDir, "engine"))
	if err != nil {
		return engineFailure(err)
	}
	if runtime.Version != params.EngineVersion {
		return &task.ExecutionError{Code: "proofread_engine_failed", Message: "proofreading engine version changed after task creation"}
	}
	stateDir := filepath.Join(workDir, "proofread-state")
	if err := progress.Report(ctx, "initialize", 0, 1); err != nil {
		return err
	}
	if _, err := e.python(ctx, runtime, "init", "--input", sourcePath, "--state-dir", stateDir, "--batch-chars", strconv.Itoa(params.BatchSize), "--overlap-chars", "600"); err != nil {
		return engineFailure(err)
	}
	batches, err := readJSONLines[batchRecord](filepath.Join(stateDir, "batches.jsonl"))
	if err != nil {
		return engineFailure(err)
	}
	if err := e.reviewBatches(ctx, runtime, stateDir, params, batches, model, progress); err != nil {
		return err
	}
	if params.Format == "epub" {
		if err := e.reviewImages(ctx, runtime, stateDir, model, progress); err != nil {
			return err
		}
	}
	if err := e.verifyCandidates(ctx, runtime, stateDir, params, model, progress); err != nil {
		return err
	}
	statusResult, err := e.python(ctx, runtime, "status", "--state-dir", stateDir)
	if err != nil {
		return engineFailure(err)
	}
	var status struct {
		ReadyToApply bool `json:"ready_to_apply"`
	}
	if err := json.Unmarshal([]byte(statusResult.Stdout), &status); err != nil || !status.ReadyToApply {
		return &task.ExecutionError{Code: "proofread_engine_failed", Message: "proofreading engine did not reach its completion gate", Err: err}
	}
	if _, err := e.python(ctx, runtime, "verify", "--state-dir", stateDir); err != nil {
		return engineFailure(err)
	}
	if err := copyTree(runtime.Root, filepath.Join(stateDir, "engine")); err != nil {
		return engineFailure(err)
	}
	candidates, err := e.databaseCandidates(stateDir)
	if err != nil {
		return engineFailure(err)
	}
	runID, err := store.NewID()
	if err != nil {
		return err
	}
	run := store.ProofreadRunRecord{
		ID: runID, BookID: value.BookID, SourceFileID: source.ID, TaskID: value.ID, SourceSHA256: source.SHA256,
		Format: params.Format, Model: params.Model, BatchSize: params.BatchSize, Concurrency: params.Concurrency,
		EngineVersion: params.EngineVersion, CreatedAt: value.CreatedAt,
	}
	if err := e.store.CommitProofreadRun(ctx, run, candidates, filepath.ToSlash(filepath.Join(workRel, "proofread-state")), e.now()); err != nil {
		return engineFailure(err)
	}
	return nil
}

func (e *Executor) releaseEngine(format, root string) (enginebundle.Runtime, error) {
	if format == "epub" {
		return epubengine.Release(root)
	}
	return novelengine.Release(root)
}

func (e *Executor) python(ctx context.Context, runtime enginebundle.Runtime, args ...string) (CommandResult, error) {
	result, err := e.runner.Run(ctx, CommandSpec{Path: "python3", Dir: runtime.Root, Args: append([]string{runtime.MainScript}, args...), Timeout: 10 * time.Minute})
	return result, err
}

func (e *Executor) reviewBatches(ctx context.Context, runtime enginebundle.Runtime, stateDir string, params Parameters, batches []batchRecord, model ModelClient, progress task.ProgressReporter) error {
	for start := 0; start < len(batches); start += params.Concurrency {
		end := min(start+params.Concurrency, len(batches))
		wave := batches[start:end]
		outputs, err := runOrdered(ctx, len(wave), params.Concurrency, func(ctx context.Context, index int) (firstReviewOutput, error) {
			shown, err := e.python(ctx, runtime, "show-batch", "--state-dir", stateDir, "--batch-id", wave[index].BatchID)
			if err != nil {
				return firstReviewOutput{}, engineFailure(err)
			}
			schema := FirstReviewSchema
			if params.Format == "epub" {
				schema = EPUBFirstReviewSchema
			}
			var output firstReviewOutput
			err = model.Invoke(ctx, CodexRequest{WorkDir: stateDir, Prompt: firstReviewPrompt(params.Format, shown.Stdout), Schema: schema}, &output)
			return output, modelFailure(err)
		})
		if err != nil {
			return err
		}
		glossaryVersion, err := readGlossaryVersion(stateDir)
		if err != nil {
			return engineFailure(err)
		}
		var glossary []glossaryProposal
		for index, output := range outputs {
			candidateFile := filepath.Join(stateDir, fmt.Sprintf("first-review-%05d.jsonl", start+index+1))
			if err := writeJSONLines(candidateFile, output.Candidates); err != nil {
				return engineFailure(err)
			}
			if _, err := e.python(ctx, runtime, "import-candidates", "--state-dir", stateDir, "--file", candidateFile); err != nil {
				return engineFailure(err)
			}
			if _, err := e.python(ctx, runtime, "complete-batch", "--state-dir", stateDir, "--batch-id", wave[index].BatchID, "--reviewer", "codex-cli", "--glossary-version", strconv.Itoa(glossaryVersion)); err != nil {
				return engineFailure(err)
			}
			glossary = append(glossary, output.Glossary...)
		}
		if len(glossary) != 0 {
			filename := filepath.Join(stateDir, fmt.Sprintf("glossary-wave-%05d.json", start+1))
			data, _ := json.Marshal(glossary)
			if err := os.WriteFile(filename, data, 0o600); err != nil {
				return engineFailure(err)
			}
			if _, err := e.python(ctx, runtime, "merge-glossary", "--state-dir", stateDir, "--file", filename); err != nil {
				return engineFailure(err)
			}
		}
		if err := progress.Report(ctx, "first_review", end, len(batches)); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) reviewImages(ctx context.Context, runtime enginebundle.Runtime, stateDir string, model ModelClient, progress task.ProgressReporter) error {
	images, err := readJSONLines[imageRecord](filepath.Join(stateDir, "images.jsonl"))
	if err != nil {
		return engineFailure(err)
	}
	for index, image := range images {
		if image.Status != "pending" {
			continue
		}
		shown, err := e.python(ctx, runtime, "show-image", "--state-dir", stateDir, "--image-id", image.ImageID)
		if err != nil {
			return engineFailure(err)
		}
		var payload struct {
			ExtractedPath string `json:"extracted_path"`
		}
		if err := json.Unmarshal([]byte(shown.Stdout), &payload); err != nil {
			return engineFailure(err)
		}
		if payload.ExtractedPath == "" {
			if _, err := e.python(ctx, runtime, "skip-image", "--state-dir", stateDir, "--image-id", image.ImageID, "--reviewer", "kindle-go", "--reason", "image could not be extracted for inspection"); err != nil {
				return engineFailure(err)
			}
			continue
		}
		var output imageReviewOutput
		if err := model.Invoke(ctx, CodexRequest{WorkDir: stateDir, Prompt: imageReviewPrompt(shown.Stdout), Schema: ImageReviewSchema, Images: []string{payload.ExtractedPath}}, &output); err != nil {
			return modelFailure(err)
		}
		filename := filepath.Join(stateDir, fmt.Sprintf("image-review-%05d.jsonl", index+1))
		if err := writeJSONLines(filename, output.Candidates); err != nil {
			return engineFailure(err)
		}
		if _, err := e.python(ctx, runtime, "import-candidates", "--state-dir", stateDir, "--file", filename); err != nil {
			return engineFailure(err)
		}
		if _, err := e.python(ctx, runtime, "complete-image", "--state-dir", stateDir, "--image-id", image.ImageID, "--reviewer", "codex-cli"); err != nil {
			return engineFailure(err)
		}
		if err := progress.Report(ctx, "image_review", index+1, len(images)); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) verifyCandidates(ctx context.Context, runtime enginebundle.Runtime, stateDir string, params Parameters, model ModelClient, progress task.ProgressReporter) error {
	candidates, err := readJSONMaps(filepath.Join(stateDir, "candidates.jsonl"))
	if err != nil {
		return engineFailure(err)
	}
	for start := 0; start < len(candidates); start += params.Concurrency {
		end := min(start+params.Concurrency, len(candidates))
		wave := candidates[start:end]
		outputs, err := runOrdered(ctx, len(wave), params.Concurrency, func(ctx context.Context, index int) (verificationOutput, error) {
			candidateID := stringValue(wave[index], "candidate_id")
			shown, err := e.python(ctx, runtime, "show-review", "--state-dir", stateDir, "--candidate-id", candidateID)
			if err != nil {
				return verificationOutput{}, engineFailure(err)
			}
			var payload struct {
				ImagePath string `json:"image_path"`
			}
			_ = json.Unmarshal([]byte(shown.Stdout), &payload)
			request := CodexRequest{WorkDir: stateDir, Prompt: verificationPrompt(shown.Stdout), Schema: VerificationSchema}
			if payload.ImagePath != "" {
				request.Images = []string{payload.ImagePath}
			}
			var output verificationOutput
			if err := model.Invoke(ctx, request, &output); err != nil {
				return verificationOutput{}, modelFailure(err)
			}
			output.CandidateID = candidateID
			output.ReviewMode = "same_model_isolated"
			output.Reviewer = "codex-cli"
			return output, nil
		})
		if err != nil {
			return err
		}
		filename := filepath.Join(stateDir, fmt.Sprintf("verification-wave-%05d.jsonl", start+1))
		if err := writeJSONLines(filename, outputs); err != nil {
			return engineFailure(err)
		}
		if _, err := e.python(ctx, runtime, "import-reviews", "--state-dir", stateDir, "--file", filename); err != nil {
			return engineFailure(err)
		}
		if err := progress.Report(ctx, "verification", end, len(candidates)); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) databaseCandidates(stateDir string) ([]store.ProofreadCandidateRecord, error) {
	candidates, err := readJSONMaps(filepath.Join(stateDir, "candidates.jsonl"))
	if err != nil {
		return nil, err
	}
	reviews, err := readJSONMaps(filepath.Join(stateDir, "reviews.jsonl"))
	if err != nil {
		return nil, err
	}
	reviewByID := map[string]map[string]any{}
	for _, review := range reviews {
		reviewByID[stringValue(review, "candidate_id")] = review
	}
	result := make([]store.ProofreadCandidateRecord, 0, len(candidates))
	for _, candidate := range candidates {
		id := stringValue(candidate, "candidate_id")
		review := reviewByID[id]
		location := map[string]any{}
		for _, key := range []string{"line", "start_char", "end_char", "block_id", "document_href", "start_offset", "end_offset", "image_id", "image_usage_id", "resource_href", "raw_start", "raw_end"} {
			if value, ok := candidate[key]; ok {
				location[key] = value
			}
		}
		locationJSON, err := json.Marshal(location)
		if err != nil {
			return nil, err
		}
		kind := stringValue(candidate, "kind")
		if kind == "" {
			kind = "text"
		}
		result = append(result, store.ProofreadCandidateRecord{
			ID: id, Kind: kind, LocationJSON: string(locationJSON), ExpectedOriginal: stringValue(candidate, "original"),
			Category: stringValue(candidate, "category"), FirstConfidence: stringValue(candidate, "confidence"), FirstReplacement: stringValue(candidate, "proposed"),
			Verification: stringValue(review, "verdict"), VerifiedReplacement: stringValue(review, "proposed"), Reason: stringValue(candidate, "reason"), CreatedAt: e.now(),
		})
	}
	return result, nil
}

func firstReviewPrompt(format, payload string) []byte {
	location := "source line"
	if format == "epub" {
		location = "EPUB block_id"
	}
	return []byte("Conservatively proofread every core span in the supplied payload. Discover contextual wrong, missing, duplicate, or homophone characters and confirmed out-of-story site ads. Preserve voice; grammar, punctuation, style, and ambiguous wording must be review confidence. Return candidates only for the core range using the exact " + location + ". Hints are not decisions. Return only schema-valid JSON.\n\n" + payload)
}

func imageReviewPrompt(payload string) []byte {
	return []byte("Inspect the attached image only for out-of-story site advertising. Do not typo-proofread ordinary cover, chart, diagram, or illustration text. Return one candidate per affected usage, or an empty candidates array. Return only schema-valid JSON.\n\n" + payload)
}

func verificationPrompt(payload string) []byte {
	return []byte("Independently verify this single candidate from source context. Do not infer or request the first review confidence or rationale. Return high only when the proposed replacement is unambiguous; otherwise review or reject. Return only schema-valid JSON.\n\n" + payload)
}

func engineFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if commandNotFound(err) {
		return &task.ExecutionError{Code: "python_not_found", Message: "python3 was not found", Err: err}
	}
	return &task.ExecutionError{Code: "proofread_engine_failed", Err: err}
}

func modelFailure(err error) error {
	if err == nil {
		return nil
	}
	var codexErr *CodexError
	if errors.As(err, &codexErr) {
		return &task.ExecutionError{Code: codexErr.Code, Message: codexErr.Message, Diagnostic: codexErr.Stderr, Err: err}
	}
	return &task.ExecutionError{Code: "codex_unavailable", Err: err}
}

func readGlossaryVersion(stateDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "glossary.json"))
	if err != nil {
		return 0, err
	}
	var value struct{ Version int }
	err = json.Unmarshal(data, &value)
	return value.Version, err
}

func readJSONLines[T any](filename string) ([]T, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var result []T
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var value T
		if err := json.Unmarshal(line, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func readJSONMaps(filename string) ([]map[string]any, error) {
	return readJSONLines[map[string]any](filename)
}

func writeJSONLines[T any](filename string, values []T) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return err
		}
	}
	return os.WriteFile(filename, buffer.Bytes(), 0o600)
}

func stringValue(value map[string]any, key string) string {
	if value == nil || value[key] == nil {
		return ""
	}
	return fmt.Sprint(value[key])
}

func runOrdered[T any](ctx context.Context, count, concurrency int, run func(context.Context, int) (T, error)) ([]T, error) {
	if concurrency < 1 {
		concurrency = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]T, count)
	jobs := make(chan int)
	var wait sync.WaitGroup
	var lock sync.Mutex
	var firstErr error
	for worker := 0; worker < min(concurrency, count); worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				value, err := run(ctx, index)
				if err != nil {
					lock.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					lock.Unlock()
					continue
				}
				results[index] = value
			}
		}()
	}
sendJobs:
	for index := 0; index < count; index++ {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wait.Wait()
	return results, firstErr
}
