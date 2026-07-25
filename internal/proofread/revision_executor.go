package proofread

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/epub"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

type RevisionExecutor struct {
	store   *store.Store
	library *library.Service
	runner  CommandRunner
	now     func() time.Time
}

func NewRevisionExecutor(storage *store.Store, libraryService *library.Service, runner CommandRunner) *RevisionExecutor {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return &RevisionExecutor{store: storage, library: libraryService, runner: runner, now: time.Now}
}

func (e *RevisionExecutor) Execute(ctx context.Context, value task.Task, progress task.ProgressReporter) error {
	params, err := decodeRevisionParameters(value.ParametersJSON)
	if err != nil {
		return &task.ExecutionError{Code: "invalid_task_parameters", Err: err}
	}
	if params.Format != "txt" && params.Format != "epub" {
		return &task.ExecutionError{Code: "invalid_task_parameters", Message: "unsupported revision format"}
	}
	if (value.Type == task.BuildRevisionTXT && params.Format != "txt") || (value.Type == task.BuildRevisionEPUB && params.Format != "epub") {
		return &task.ExecutionError{Code: "invalid_task_parameters", Message: "revision task type does not match format"}
	}
	if err := progress.Report(ctx, "prepare", 0, 5); err != nil {
		return err
	}
	run, ok, err := e.store.ProofreadRun(ctx, params.RunID)
	if err != nil || !ok || run.Status != "completed" || run.BookID != value.BookID || run.SourceFileID != value.InputFileID || params.SourceFileID != value.InputFileID || run.SourceSHA256 != params.SourceSHA256 || run.Format != params.Format || run.EngineVersion != params.EngineVersion {
		return &task.ExecutionError{Code: "revision_state_mismatch", Message: "proofread run no longer matches the revision snapshot", Err: err}
	}
	_, source, err := e.library.ResolveOriginal(ctx, value.BookID, value.InputFileID, params.SourceSHA256)
	if err != nil {
		return &task.ExecutionError{Code: "source_hash_mismatch", Err: err}
	}
	candidates, err := e.store.ProofreadCandidates(ctx, run.ID)
	if err != nil {
		return err
	}
	if err := validateRevisionSnapshot(candidates, params.Decisions); err != nil {
		return &task.ExecutionError{Code: "revision_conflict", Err: err}
	}
	workRel := filepath.ToSlash(filepath.Join("work", value.ID))
	workDir, err := e.store.ResolveRel(workRel)
	if err != nil {
		return err
	}
	defer e.library.RemoveTaskWork(value.ID)
	stateSource, err := e.store.ResolveRel(run.EngineStateRelPath)
	if err != nil {
		return err
	}
	stateDir := filepath.Join(workDir, "revision-state")
	if err := copyTree(stateSource, stateDir); err != nil {
		return revisionFailure(err)
	}
	runtime, err := persistedRuntime(stateDir, params.Format, params.EngineVersion)
	if err != nil {
		return revisionFailure(err)
	}
	if runtime.Version != params.EngineVersion {
		return &task.ExecutionError{Code: "revision_state_mismatch", Message: "proofreading engine version changed after review"}
	}
	if err := os.WriteFile(filepath.Join(stateDir, "decisions.jsonl"), nil, 0o600); err != nil {
		return revisionFailure(err)
	}
	decisionFile := filepath.Join(workDir, "frozen-decisions.jsonl")
	if err := writeJSONLines(decisionFile, scriptDecisions(params.Decisions)); err != nil {
		return revisionFailure(err)
	}
	if _, err := e.python(ctx, runtime, "import-decisions", "--state-dir", stateDir, "--file", decisionFile); err != nil {
		return revisionFailure(err)
	}
	if err := progress.Report(ctx, "apply", 2, 5); err != nil {
		return err
	}
	outputRel := filepath.ToSlash(filepath.Join(workRel, "revision-output"))
	outputDir := filepath.Join(workDir, "revision-output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return revisionFailure(err)
	}
	outputPath := filepath.Join(outputDir, "revision."+params.Format)
	reportPath := filepath.Join(outputDir, "report.md")
	auditPath := filepath.Join(outputDir, "audit.jsonl")
	result, err := e.runner.Run(ctx, CommandSpec{
		Path: "python3", Dir: runtime.Root, Timeout: 20 * time.Minute,
		Args: []string{runtime.ApplyScript, "--state-dir", stateDir, "--output", outputPath, "--report", reportPath, "--audit", auditPath},
	})
	if err != nil {
		return revisionFailure(fmt.Errorf("apply revision: %w: %s", err, strings.TrimSpace(result.Stderr)))
	}
	if err := progress.Report(ctx, "verify", 4, 5); err != nil {
		return err
	}
	var compatibility *store.CompatibilityRecord
	if params.Format == "epub" {
		record, err := compatibilityRecord(outputPath, e.now())
		if err != nil {
			return revisionFailure(err)
		}
		compatibility = &record
	}
	if _, _, err := e.library.ResolveOriginal(ctx, value.BookID, value.InputFileID, params.SourceSHA256); err != nil {
		return &task.ExecutionError{Code: "source_hash_mismatch", Err: err}
	}
	base := strings.TrimSuffix(source.DisplayName, filepath.Ext(source.DisplayName))
	if strings.TrimSpace(base) == "" {
		base = "book"
	}
	stamp := value.CreatedAt.Local().Format("20060102-150405")
	commit := store.RevisionCommit{
		BookID: value.BookID, SourceFileID: value.InputFileID, TaskID: value.ID, ProofreadRunID: run.ID,
		Format: params.Format, RevisionName: fmt.Sprintf("%s-revised-%s.%s", base, stamp, params.Format),
		ReportName: fmt.Sprintf("%s-proofread-%s.md", base, stamp), AuditName: fmt.Sprintf("%s-proofread-%s.jsonl", base, stamp),
		WorkDirRel: outputRel, ParametersJSON: value.ParametersJSON, HasUnresolved: params.HasUnresolved, CreatedAt: value.CreatedAt,
		Compatibility: compatibility,
	}
	for _, decision := range params.Decisions {
		commit.Candidates = append(commit.Candidates, store.RevisionCandidateSnapshot{CandidateID: decision.CandidateID, Outcome: decision.Outcome, Replacement: decision.Replacement})
	}
	if err := progress.Report(ctx, "finalize", 5, 5); err != nil {
		return err
	}
	if _, err := e.store.CommitRevision(ctx, commit); err != nil {
		return &task.ExecutionError{Code: "revision_commit_failed", Err: err}
	}
	return nil
}

func (e *RevisionExecutor) python(ctx context.Context, runtime engineRuntime, args ...string) (CommandResult, error) {
	return e.runner.Run(ctx, CommandSpec{Path: "python3", Dir: runtime.Root, Args: append([]string{runtime.MainScript}, args...), Timeout: 10 * time.Minute})
}

type engineRuntime struct {
	Version, Root, MainScript, ApplyScript string
}

func persistedRuntime(stateDir, format, expectedVersion string) (engineRuntime, error) {
	root := filepath.Join(stateDir, "engine")
	versionData, err := os.ReadFile(filepath.Join(root, "ENGINE_VERSION"))
	if err != nil {
		return engineRuntime{}, err
	}
	version := strings.TrimSpace(string(versionData))
	if version == "" || version != expectedVersion {
		return engineRuntime{}, fmt.Errorf("persisted proofreading engine version does not match the revision snapshot")
	}
	mainScript, applyScript := "scripts/proofread_txt.py", "scripts/apply_reviewed_edits.py"
	if format == "epub" {
		mainScript = "scripts/proofread_epub.py"
	}
	return engineRuntime{
		Version: version, Root: root, MainScript: filepath.Join(root, filepath.FromSlash(mainScript)), ApplyScript: filepath.Join(root, filepath.FromSlash(applyScript)),
	}, nil
}

func validateRevisionSnapshot(candidates []store.ProofreadCandidateRecord, decisions []RevisionDecision) error {
	if len(candidates) != len(decisions) {
		return fmt.Errorf("revision snapshot candidate count changed")
	}
	byID := make(map[string]store.ProofreadCandidateRecord, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	applying := make([]store.ProofreadCandidateRecord, 0, len(decisions))
	seen := make(map[string]bool, len(decisions))
	for _, decision := range decisions {
		candidate, ok := byID[decision.CandidateID]
		if !ok || seen[decision.CandidateID] || candidate.ExpectedOriginal != decision.ExpectedOriginal || candidate.LocationJSON != decision.LocationJSON {
			return fmt.Errorf("revision candidate %q no longer matches", decision.CandidateID)
		}
		seen[decision.CandidateID] = true
		switch decision.Outcome {
		case "apply_automatic", "apply_accepted", "apply_modified":
			if decision.Replacement == "" && candidate.Category != "external_ad" {
				return fmt.Errorf("applied candidate %q has an empty replacement", decision.CandidateID)
			}
			applying = append(applying, candidate)
		case "keep_rejected", "keep_pending", "keep_conflict":
		default:
			return fmt.Errorf("candidate %q has invalid frozen outcome", decision.CandidateID)
		}
	}
	for left := 0; left < len(applying); left++ {
		for right := left + 1; right < len(applying); right++ {
			if candidatesConflict(applying[left], applying[right]) {
				return fmt.Errorf("applied candidates %q and %q overlap", applying[left].ID, applying[right].ID)
			}
		}
	}
	return nil
}

func scriptDecisions(decisions []RevisionDecision) []map[string]string {
	result := make([]map[string]string, 0, len(decisions))
	for _, decision := range decisions {
		switch decision.Outcome {
		case "apply_accepted", "apply_modified":
			result = append(result, map[string]string{"candidate_id": decision.CandidateID, "action": "accept", "proposed": decision.Replacement, "reason": "frozen user decision"})
		case "keep_rejected":
			result = append(result, map[string]string{"candidate_id": decision.CandidateID, "action": "reject", "reason": "frozen user rejection"})
		}
	}
	return result
}

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("proofread state contains a symbolic link: %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("proofread state contains a non-regular file: %s", relative)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}

func compatibilityRecord(filename string, now time.Time) (store.CompatibilityRecord, error) {
	analysis := epub.Analyze(filename, epub.Options{DefaultLanguage: "zh-CN"})
	metadata, err := json.Marshal(struct {
		Metadata epub.MetadataInfo `json:"metadata"`
		Cover    epub.CoverInfo    `json:"cover"`
	}{analysis.Metadata, analysis.Cover})
	if err != nil {
		return store.CompatibilityRecord{}, err
	}
	spine, err := json.Marshal(analysis.Spine)
	if err != nil {
		return store.CompatibilityRecord{}, err
	}
	toc, err := json.Marshal(analysis.TOC)
	if err != nil {
		return store.CompatibilityRecord{}, err
	}
	resources, err := json.Marshal(analysis.Resources)
	if err != nil {
		return store.CompatibilityRecord{}, err
	}
	issues, err := json.Marshal(analysis.Issues)
	if err != nil {
		return store.CompatibilityRecord{}, err
	}
	status := "passed"
	if !analysis.Compatible() {
		status = "failed"
	}
	digest, _, err := hashPath(filename)
	if err != nil {
		return store.CompatibilityRecord{}, err
	}
	return store.CompatibilityRecord{
		SourceSHA256: digest, Status: status, MetadataJSON: string(metadata), SpineJSON: string(spine), TOCJSON: string(toc),
		ResourcesJSON: string(resources), IssuesJSON: string(issues), CheckedAt: now,
	}, nil
}

func hashPath(filename string) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), size, nil
}

func revisionFailure(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	if commandNotFound(err) {
		return &task.ExecutionError{Code: "python_not_found", Message: "python3 was not found", Err: err}
	}
	return &task.ExecutionError{Code: "revision_failed", Err: err}
}
