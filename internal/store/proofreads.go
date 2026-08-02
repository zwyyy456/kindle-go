package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ProofreadRunRecord struct {
	ID, BookID, SourceFileID, TaskID, SourceSHA256, Format, Model, Status, EngineVersion, EngineStateRelPath string
	BatchSize, Concurrency                                                                                   int
	CreatedAt, CompletedAt                                                                                   time.Time
}

type ProofreadCandidateRecord struct {
	ID, RunID, Kind, LocationJSON, ExpectedOriginal, Category, FirstConfidence, FirstReplacement string
	Verification, VerifiedReplacement, Reason                                                    string
	CreatedAt                                                                                    time.Time
}

type CandidateDecisionRecord struct {
	ID, CandidateID, Decision, Replacement string
	CreatedAt                              time.Time
}

func (s *Store) CommitProofreadRun(ctx context.Context, run ProofreadRunRecord, candidates []ProofreadCandidateRecord, workStateRel string, now time.Time) error {
	cleanWork := filepath.ToSlash(filepath.Clean(workStateRel))
	if run.ID == "" || run.TaskID == "" || cleanWork != filepath.ToSlash(filepath.Join("work", run.TaskID, "proofread-state")) {
		return fmt.Errorf("invalid proofread state path")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	workPath, err := s.ResolveRel(cleanWork)
	if err != nil {
		return err
	}
	finalRel := filepath.ToSlash(filepath.Join("proofreads", run.BookID, run.ID, "state"))
	finalPath, err := s.ResolveRel(finalRel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	if err := os.Rename(workPath, finalPath); err != nil {
		return err
	}
	restore := true
	defer func() {
		if restore {
			_ = os.MkdirAll(filepath.Dir(workPath), 0o755)
			_ = os.Rename(finalPath, workPath)
		}
	}()
	// Completion and cancellation compete through the task status update. Once the
	// filesystem move starts, do not let request cancellation interrupt the commit
	// halfway and expose a run whose task never reached completed.
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	completed, err := completeRunningTask(context.Background(), tx, run.TaskID, now)
	if err != nil || !completed {
		if err != nil {
			return err
		}
		return fmt.Errorf("task %q was canceled before proofread commit", run.TaskID)
	}
	_, err = tx.ExecContext(context.Background(), `INSERT INTO proofread_runs(id, book_id, source_file_id, task_id, source_sha256, format, model, batch_size, concurrency, status, engine_version, engine_state_rel_path, created_at, completed_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'completed', ?, ?, ?, ?)`, run.ID, run.BookID, run.SourceFileID, run.TaskID, run.SourceSHA256, run.Format, run.Model, run.BatchSize, run.Concurrency, run.EngineVersion, finalRel, formatTime(run.CreatedAt), formatTime(now))
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		createdAt := candidate.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		_, err := tx.ExecContext(context.Background(), `INSERT INTO proofread_candidates(id, run_id, kind, location_json, expected_original, category, first_confidence, first_replacement, verification, verified_replacement, reason, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.ID, run.ID, candidate.Kind, candidate.LocationJSON, candidate.ExpectedOriginal, candidate.Category, candidate.FirstConfidence, candidate.FirstReplacement, candidate.Verification, candidate.VerifiedReplacement, candidate.Reason, formatTime(createdAt))
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	restore = false
	return nil
}

func (s *Store) ProofreadRuns(ctx context.Context, bookID string) ([]ProofreadRunRecord, error) {
	if strings.TrimSpace(bookID) != "" {
		return s.proofreadRuns(ctx, ` WHERE book_id = ?`, bookID)
	}
	return s.proofreadRuns(ctx, "")
}

func (s *Store) ProofreadRun(ctx context.Context, id string) (ProofreadRunRecord, bool, error) {
	runs, err := s.proofreadRuns(ctx, ` WHERE id = ?`, id)
	if err != nil || len(runs) == 0 {
		return ProofreadRunRecord{}, false, err
	}
	return runs[0], true, nil
}

func (s *Store) proofreadRuns(ctx context.Context, where string, args ...any) ([]ProofreadRunRecord, error) {
	query := `SELECT id, book_id, source_file_id, task_id, source_sha256, format, model, batch_size, concurrency, status, engine_version, engine_state_rel_path, created_at, COALESCE(completed_at, '') FROM proofread_runs` + where + ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []ProofreadRunRecord
	for rows.Next() {
		var run ProofreadRunRecord
		var created, completed string
		if err := rows.Scan(&run.ID, &run.BookID, &run.SourceFileID, &run.TaskID, &run.SourceSHA256, &run.Format, &run.Model, &run.BatchSize, &run.Concurrency, &run.Status, &run.EngineVersion, &run.EngineStateRelPath, &created, &completed); err != nil {
			return nil, err
		}
		run.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		if completed != "" {
			run.CompletedAt, err = parseTime(completed)
			if err != nil {
				return nil, err
			}
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) ProofreadCandidates(ctx context.Context, runID string) ([]ProofreadCandidateRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, kind, location_json, expected_original, category, first_confidence, first_replacement, verification, verified_replacement, reason, created_at FROM proofread_candidates WHERE run_id = ? ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []ProofreadCandidateRecord
	for rows.Next() {
		var candidate ProofreadCandidateRecord
		var created string
		if err := rows.Scan(&candidate.ID, &candidate.RunID, &candidate.Kind, &candidate.LocationJSON, &candidate.ExpectedOriginal, &candidate.Category, &candidate.FirstConfidence, &candidate.FirstReplacement, &candidate.Verification, &candidate.VerifiedReplacement, &candidate.Reason, &created); err != nil {
			return nil, err
		}
		candidate.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (s *Store) ProofreadCandidate(ctx context.Context, id string) (ProofreadCandidateRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, run_id, kind, location_json, expected_original, category, first_confidence, first_replacement, verification, verified_replacement, reason, created_at FROM proofread_candidates WHERE id = ?`, id)
	var candidate ProofreadCandidateRecord
	var created string
	if err := row.Scan(&candidate.ID, &candidate.RunID, &candidate.Kind, &candidate.LocationJSON, &candidate.ExpectedOriginal, &candidate.Category, &candidate.FirstConfidence, &candidate.FirstReplacement, &candidate.Verification, &candidate.VerifiedReplacement, &candidate.Reason, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProofreadCandidateRecord{}, false, nil
		}
		return ProofreadCandidateRecord{}, false, err
	}
	var err error
	candidate.CreatedAt, err = parseTime(created)
	return candidate, err == nil, err
}

func (s *Store) CandidateDecisions(ctx context.Context, runID string) ([]CandidateDecisionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.candidate_id, d.decision, d.replacement, d.created_at
FROM candidate_decisions d JOIN proofread_candidates c ON c.id = d.candidate_id
WHERE c.run_id = ? ORDER BY d.created_at, d.id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []CandidateDecisionRecord
	for rows.Next() {
		var decision CandidateDecisionRecord
		var created string
		if err := rows.Scan(&decision.ID, &decision.CandidateID, &decision.Decision, &decision.Replacement, &created); err != nil {
			return nil, err
		}
		decision.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

func (s *Store) AppendCandidateDecision(ctx context.Context, candidateID, decision, replacement string, now time.Time) (CandidateDecisionRecord, error) {
	if decision != "accept" && decision != "reject" && decision != "modify" {
		return CandidateDecisionRecord{}, fmt.Errorf("unsupported candidate decision %q", decision)
	}
	id, err := NewID()
	if err != nil {
		return CandidateDecisionRecord{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO candidate_decisions(id, candidate_id, decision, replacement, created_at)
VALUES(?, ?, ?, ?, ?)`, id, candidateID, decision, replacement, formatTime(now))
	if err != nil {
		return CandidateDecisionRecord{}, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		if err != nil {
			return CandidateDecisionRecord{}, err
		}
		return CandidateDecisionRecord{}, fmt.Errorf("candidate decision was not inserted")
	}
	return CandidateDecisionRecord{ID: id, CandidateID: candidateID, Decision: decision, Replacement: replacement, CreatedAt: now}, nil
}
