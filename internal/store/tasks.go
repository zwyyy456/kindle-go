package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type TaskRecord struct {
	ID              string
	BookID          string
	Type            string
	Status          string
	QueueSeq        int64
	InputFileID     string
	RetryOfTaskID   string
	ParametersJSON  string
	Stage           string
	ProgressCurrent int
	ProgressTotal   int
	ErrorCode       string
	ErrorMessage    string
	CreatedAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
}

type CreateTaskParams struct {
	BookID         string
	Type           string
	InputFileID    string
	RetryOfTaskID  string
	ParametersJSON string
	CreatedAt      time.Time
}

func (s *Store) CreateTask(ctx context.Context, params CreateTaskParams) (TaskRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskRecord{}, err
	}
	defer tx.Rollback()
	record, err := createTask(ctx, tx, params)
	if err != nil {
		return TaskRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskRecord{}, err
	}
	return record, nil
}

func (s *Store) CreateTasks(ctx context.Context, params []CreateTaskParams) ([]TaskRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	records := make([]TaskRecord, 0, len(params))
	for _, value := range params {
		record, err := createTask(ctx, tx, value)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return records, nil
}

func createTask(ctx context.Context, tx *sql.Tx, params CreateTaskParams) (TaskRecord, error) {
	id, err := NewID()
	if err != nil {
		return TaskRecord{}, err
	}
	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	parameters := strings.TrimSpace(params.ParametersJSON)
	if parameters == "" {
		parameters = "{}"
	}
	var queueSeq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(queue_seq), 0) + 1 FROM tasks`).Scan(&queueSeq); err != nil {
		return TaskRecord{}, err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO tasks(id, book_id, type, status, queue_seq, input_file_id, retry_of_task_id, parameters_json, created_at)
VALUES(?, ?, ?, 'queued', ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
`, id, params.BookID, params.Type, queueSeq, params.InputFileID, params.RetryOfTaskID, parameters, formatTime(createdAt))
	if err != nil {
		return TaskRecord{}, err
	}
	return TaskRecord{ID: id, BookID: params.BookID, Type: params.Type, Status: "queued", QueueSeq: queueSeq, InputFileID: params.InputFileID, RetryOfTaskID: params.RetryOfTaskID, ParametersJSON: parameters, CreatedAt: createdAt}, nil
}

func (s *Store) Task(ctx context.Context, id string) (TaskRecord, bool, error) {
	record, err := scanTask(s.db.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRecord{}, false, nil
	}
	return record, err == nil, err
}

func (s *Store) ListTasks(ctx context.Context, bookID string) ([]TaskRecord, error) {
	query := taskSelect
	var args []any
	if bookID != "" {
		query += ` WHERE book_id = ?`
		args = append(args, bookID)
	}
	query += ` ORDER BY queue_seq DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []TaskRecord
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) ClaimNextTask(ctx context.Context, types []string, now time.Time) (TaskRecord, bool, error) {
	if len(types) == 0 {
		return TaskRecord{}, false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskRecord{}, false, err
	}
	defer tx.Rollback()
	placeholders := make([]string, len(types))
	args := make([]any, len(types))
	for index, value := range types {
		placeholders[index] = "?"
		args[index] = value
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM tasks WHERE status = 'queued' AND type IN (`+strings.Join(placeholders, ",")+`) ORDER BY queue_seq LIMIT 1`, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRecord{}, false, nil
	}
	if err != nil {
		return TaskRecord{}, false, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET status = 'running', started_at = ?, stage = 'prepare' WHERE id = ? AND status = 'queued'`, formatTime(now), id)
	if err != nil {
		return TaskRecord{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return TaskRecord{}, false, err
	}
	record, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id))
	if err != nil {
		return TaskRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TaskRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) UpdateTaskProgress(ctx context.Context, id, stage string, current, total int) error {
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET stage = ?, progress_current = ?, progress_total = ? WHERE id = ? AND status = 'running'`, stage, current, total, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("task %q is not running", id)
	}
	return nil
}

func (s *Store) FinishTask(ctx context.Context, id, status, code, message string, now time.Time) (bool, error) {
	if now.IsZero() {
		now = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET status = ?, error_code = ?, error_message = ?, finished_at = ?, stage = ? WHERE id = ? AND status = 'running'`, status, code, message, formatTime(now), status, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *Store) CancelTask(ctx context.Context, id string, now time.Time) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = ?`, id).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	if status != "queued" && status != "running" {
		return status, false, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET status = 'canceled', error_code = 'task_canceled', error_message = 'canceled by user', finished_at = ?, stage = 'canceled' WHERE id = ? AND status = ?`, formatTime(now), id, status)
	if err != nil {
		return status, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return status, false, err
	}
	if err := tx.Commit(); err != nil {
		return status, false, err
	}
	return status, true, nil
}

func (s *Store) RetryTask(ctx context.Context, id string, now time.Time) (TaskRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskRecord{}, err
	}
	defer tx.Rollback()
	original, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id))
	if err != nil {
		return TaskRecord{}, err
	}
	if original.Status != "failed" && original.Status != "canceled" {
		return TaskRecord{}, fmt.Errorf("task %q with status %s cannot be retried", id, original.Status)
	}
	retry, err := createTask(ctx, tx, CreateTaskParams{BookID: original.BookID, Type: original.Type, InputFileID: original.InputFileID, RetryOfTaskID: original.ID, ParametersJSON: original.ParametersJSON, CreatedAt: now})
	if err != nil {
		return TaskRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskRecord{}, err
	}
	return retry, nil
}

func (s *Store) RecoverRunningTasks(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET status = 'failed', error_code = 'task_process_interrupted', error_message = 'server stopped while task was running; retry starts from the beginning', finished_at = ?, stage = 'failed' WHERE status = 'running'`, formatTime(now))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

const taskSelect = `SELECT id, book_id, type, status, queue_seq, COALESCE(input_file_id, ''), COALESCE(retry_of_task_id, ''), parameters_json, stage, progress_current, progress_total, error_code, error_message, created_at, COALESCE(started_at, ''), COALESCE(finished_at, '') FROM tasks`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (TaskRecord, error) {
	var record TaskRecord
	var created, started, finished string
	err := row.Scan(&record.ID, &record.BookID, &record.Type, &record.Status, &record.QueueSeq, &record.InputFileID, &record.RetryOfTaskID, &record.ParametersJSON, &record.Stage, &record.ProgressCurrent, &record.ProgressTotal, &record.ErrorCode, &record.ErrorMessage, &created, &started, &finished)
	if err != nil {
		return TaskRecord{}, err
	}
	record.CreatedAt, err = parseTime(created)
	if err != nil {
		return TaskRecord{}, err
	}
	if started != "" {
		record.StartedAt, err = parseTime(started)
		if err != nil {
			return TaskRecord{}, err
		}
	}
	if finished != "" {
		record.FinishedAt, err = parseTime(finished)
		if err != nil {
			return TaskRecord{}, err
		}
	}
	return record, nil
}
