package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RevisionCandidateSnapshot struct {
	CandidateID, Outcome, Replacement string
}

type RevisionCommit struct {
	BookID, SourceFileID, TaskID, ProofreadRunID string
	Format, RevisionName, ReportName, AuditName  string
	WorkDirRel, ParametersJSON                   string
	HasUnresolved                                bool
	CreatedAt                                    time.Time
	Candidates                                   []RevisionCandidateSnapshot
	Compatibility                                *CompatibilityRecord
}

type RevisionCommitResult struct {
	Revision, Report, Audit File
}

func (s *Store) CommitRevision(ctx context.Context, commit RevisionCommit) (RevisionCommitResult, error) {
	cleanWork := filepath.ToSlash(filepath.Clean(commit.WorkDirRel))
	if commit.TaskID == "" || cleanWork != filepath.ToSlash(filepath.Join("work", commit.TaskID, "revision-output")) {
		return RevisionCommitResult{}, fmt.Errorf("invalid revision work directory")
	}
	format := strings.ToLower(strings.TrimSpace(commit.Format))
	if format != "txt" && format != "epub" {
		return RevisionCommitResult{}, fmt.Errorf("invalid revision format %q", format)
	}
	workDir, err := s.ResolveRel(cleanWork)
	if err != nil {
		return RevisionCommitResult{}, err
	}
	type staged struct {
		role, format, name, workName string
		id, relPath, digest          string
		size                         int64
	}
	values := []staged{
		{role: "revision", format: format, name: commit.RevisionName, workName: "revision." + format},
		{role: "report", format: "md", name: commit.ReportName, workName: "report.md"},
		{role: "audit", format: "jsonl", name: commit.AuditName, workName: "audit.jsonl"},
	}
	for index := range values {
		values[index].id, err = NewID()
		if err != nil {
			return RevisionCommitResult{}, err
		}
		values[index].digest, values[index].size, err = hashFile(filepath.Join(workDir, values[index].workName))
		if err != nil {
			return RevisionCommitResult{}, err
		}
	}
	if format == "epub" {
		if commit.Compatibility == nil || commit.Compatibility.SourceSHA256 != values[0].digest {
			return RevisionCommitResult{}, fmt.Errorf("EPUB revision requires a matching compatibility report")
		}
	} else if commit.Compatibility != nil {
		return RevisionCommitResult{}, fmt.Errorf("TXT revision must not include an EPUB compatibility report")
	}
	finalDirRel := filepath.ToSlash(filepath.Join("revisions", commit.BookID, values[0].id))
	finalDir, err := s.ResolveRel(finalDirRel)
	if err != nil {
		return RevisionCommitResult{}, err
	}
	for index := range values {
		values[index].relPath = filepath.ToSlash(filepath.Join(finalDirRel, values[index].workName))
	}
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return RevisionCommitResult{}, err
	}
	createdAt := commit.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	parameters := strings.TrimSpace(commit.ParametersJSON)
	if parameters == "" {
		parameters = "{}"
	}
	pendingTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RevisionCommitResult{}, err
	}
	defer pendingTx.Rollback()
	for _, value := range values {
		unresolved := 0
		if value.role == "revision" && commit.HasUnresolved {
			unresolved = 1
		}
		_, err = pendingTx.ExecContext(ctx, `INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, source_file_id, task_id, proofread_run_id, parameters_json, has_unresolved, created_at)
VALUES(?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)`, value.id, commit.BookID, value.role, value.format, value.name, value.relPath, value.digest, value.size, commit.SourceFileID, commit.TaskID, commit.ProofreadRunID, parameters, unresolved, formatTime(createdAt))
		if err != nil {
			return RevisionCommitResult{}, err
		}
	}
	for _, candidate := range commit.Candidates {
		if _, err := pendingTx.ExecContext(ctx, `INSERT INTO revision_candidates(revision_file_id, candidate_id, outcome, replacement) VALUES(?, ?, ?, ?)`, values[0].id, candidate.CandidateID, candidate.Outcome, candidate.Replacement); err != nil {
			return RevisionCommitResult{}, err
		}
	}
	if commit.Compatibility != nil {
		record := *commit.Compatibility
		if record.ID == "" {
			record.ID, err = NewID()
			if err != nil {
				return RevisionCommitResult{}, err
			}
		}
		if record.CheckedAt.IsZero() {
			record.CheckedAt = createdAt
		}
		_, err = pendingTx.ExecContext(ctx, `INSERT INTO compatibility_reports(id, file_id, source_sha256, status, metadata_json, spine_json, toc_json, resources_json, issues_json, checked_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.ID, values[0].id, record.SourceSHA256, record.Status, record.MetadataJSON, record.SpineJSON, record.TOCJSON, record.ResourcesJSON, record.IssuesJSON, formatTime(record.CheckedAt))
		if err != nil {
			return RevisionCommitResult{}, err
		}
	}
	if err := pendingTx.Commit(); err != nil {
		return RevisionCommitResult{}, err
	}
	cleanupPending := func() {
		_ = os.RemoveAll(finalDir)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM files WHERE task_id = ? AND state = 'pending'`, commit.TaskID)
	}
	if err := os.Rename(workDir, finalDir); err != nil {
		cleanupPending()
		return RevisionCommitResult{}, err
	}
	finalizeTx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		cleanupPending()
		return RevisionCommitResult{}, err
	}
	defer finalizeTx.Rollback()
	result, err := finalizeTx.ExecContext(context.Background(), `UPDATE tasks SET status = 'completed', error_code = '', error_message = '', finished_at = ?, stage = 'completed' WHERE id = ? AND status = 'running'`, formatTime(time.Now()), commit.TaskID)
	if err != nil {
		_ = finalizeTx.Rollback()
		cleanupPending()
		return RevisionCommitResult{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		_ = finalizeTx.Rollback()
		cleanupPending()
		if err != nil {
			return RevisionCommitResult{}, err
		}
		return RevisionCommitResult{}, fmt.Errorf("task %q was canceled before revision commit", commit.TaskID)
	}
	if _, err := finalizeTx.ExecContext(context.Background(), `UPDATE files SET state = 'ready' WHERE task_id = ? AND state = 'pending'`, commit.TaskID); err != nil {
		_ = finalizeTx.Rollback()
		cleanupPending()
		return RevisionCommitResult{}, err
	}
	if err := finalizeTx.Commit(); err != nil {
		cleanupPending()
		return RevisionCommitResult{}, err
	}
	files := make([]File, len(values))
	for index, value := range values {
		files[index] = File{
			ID: value.id, BookID: commit.BookID, Role: value.role, State: "ready", Format: value.format, DisplayName: value.name,
			RelPath: value.relPath, SHA256: value.digest, Size: value.size, SourceFileID: commit.SourceFileID, TaskID: commit.TaskID,
			ProofreadRunID: commit.ProofreadRunID, ParametersJSON: parameters, HasUnresolved: value.role == "revision" && commit.HasUnresolved, CreatedAt: createdAt,
		}
	}
	return RevisionCommitResult{Revision: files[0], Report: files[1], Audit: files[2]}, nil
}

func (s *Store) RevisionCandidates(ctx context.Context, revisionFileID string) ([]RevisionCandidateSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT candidate_id, outcome, replacement FROM revision_candidates WHERE revision_file_id = ? ORDER BY candidate_id`, revisionFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RevisionCandidateSnapshot
	for rows.Next() {
		var value RevisionCandidateSnapshot
		if err := rows.Scan(&value.CandidateID, &value.Outcome, &value.Replacement); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
