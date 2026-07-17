package store

import (
	"context"
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
	result, err := tx.ExecContext(context.Background(), `UPDATE tasks SET status = 'completed', error_code = '', error_message = '', finished_at = ?, stage = 'completed' WHERE id = ? AND status = 'running'`, formatTime(now), run.TaskID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
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
	query := `SELECT id, book_id, source_file_id, task_id, source_sha256, format, model, batch_size, concurrency, status, engine_version, engine_state_rel_path, created_at, COALESCE(completed_at, '') FROM proofread_runs`
	var args []any
	if strings.TrimSpace(bookID) != "" {
		query += ` WHERE book_id = ?`
		args = append(args, bookID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
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
