package store

import (
	"context"
	"database/sql"
	"fmt"
)

const schemaVersion = 1

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema migrations: %w", err)
	}

	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("library schema version %d is newer than supported version %d", current, schemaVersion)
	}
	if current == schemaVersion {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE settings (
            key TEXT PRIMARY KEY,
            value_json TEXT NOT NULL,
            updated_at TEXT NOT NULL
        )`,
		`CREATE TABLE runtime_lock (
            singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
            instance_id TEXT NOT NULL,
            pid INTEGER NOT NULL,
            heartbeat_at TEXT NOT NULL
        )`,
		`CREATE TABLE books (
            id TEXT PRIMARY KEY,
            display_name TEXT NOT NULL,
            source_format TEXT NOT NULL,
            state TEXT NOT NULL CHECK (state IN ('active', 'deleting')) DEFAULT 'active',
            imported_at TEXT NOT NULL
        )`,
		`CREATE TABLE tasks (
            id TEXT PRIMARY KEY,
            book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
            type TEXT NOT NULL CHECK (type IN ('proofread', 'build_revision_txt', 'build_revision_epub', 'generate_epub', 'generate_azw3')),
            status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'canceled')),
            queue_seq INTEGER NOT NULL UNIQUE,
            input_file_id TEXT,
            retry_of_task_id TEXT REFERENCES tasks(id),
            parameters_json TEXT NOT NULL DEFAULT '{}',
            stage TEXT NOT NULL DEFAULT '',
            progress_current INTEGER NOT NULL DEFAULT 0,
            progress_total INTEGER NOT NULL DEFAULT 0,
            error_code TEXT NOT NULL DEFAULT '',
            error_message TEXT NOT NULL DEFAULT '',
            created_at TEXT NOT NULL,
            started_at TEXT,
            finished_at TEXT
        )`,
		`CREATE TABLE files (
            id TEXT PRIMARY KEY,
            book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
            role TEXT NOT NULL CHECK (role IN ('original', 'revision', 'artifact', 'report', 'audit')),
            state TEXT NOT NULL CHECK (state IN ('pending', 'ready', 'deleting')),
            format TEXT NOT NULL,
            display_name TEXT NOT NULL,
            rel_path TEXT NOT NULL UNIQUE,
            sha256 TEXT NOT NULL,
            size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
            source_file_id TEXT REFERENCES files(id),
            task_id TEXT REFERENCES tasks(id),
            proofread_run_id TEXT,
            parameters_json TEXT,
            has_unresolved INTEGER NOT NULL DEFAULT 0 CHECK (has_unresolved IN (0, 1)),
            created_at TEXT NOT NULL
        )`,
		`CREATE UNIQUE INDEX files_one_original_per_book ON files(book_id) WHERE role = 'original'`,
		`CREATE INDEX files_book_role_format_created ON files(book_id, role, format, created_at DESC)`,
		`CREATE INDEX files_sha_role ON files(sha256, role)`,
		`CREATE INDEX tasks_type_status_queue ON tasks(type, status, queue_seq)`,
		`CREATE TABLE task_events (
            task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
            seq INTEGER NOT NULL,
            level TEXT NOT NULL,
            stage TEXT NOT NULL DEFAULT '',
            message TEXT NOT NULL,
            created_at TEXT NOT NULL,
            PRIMARY KEY(task_id, seq)
        )`,
		`CREATE TABLE compatibility_reports (
            id TEXT PRIMARY KEY,
            file_id TEXT NOT NULL UNIQUE REFERENCES files(id) ON DELETE CASCADE,
            source_sha256 TEXT NOT NULL,
            status TEXT NOT NULL CHECK (status IN ('passed', 'failed')),
            metadata_json TEXT NOT NULL DEFAULT '{}',
            spine_json TEXT NOT NULL DEFAULT '[]',
            toc_json TEXT NOT NULL DEFAULT '[]',
            resources_json TEXT NOT NULL DEFAULT '[]',
            issues_json TEXT NOT NULL DEFAULT '[]',
            checked_at TEXT NOT NULL
        )`,
		`CREATE TABLE proofread_runs (
            id TEXT PRIMARY KEY,
            book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
            source_file_id TEXT NOT NULL REFERENCES files(id),
            task_id TEXT NOT NULL REFERENCES tasks(id),
            source_sha256 TEXT NOT NULL,
            format TEXT NOT NULL CHECK (format IN ('txt', 'epub')),
            model TEXT NOT NULL DEFAULT '',
            batch_size INTEGER NOT NULL,
            concurrency INTEGER NOT NULL,
            status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'canceled')),
            engine_version TEXT NOT NULL,
            engine_state_rel_path TEXT NOT NULL DEFAULT '',
            created_at TEXT NOT NULL,
            completed_at TEXT
        )`,
		`CREATE TABLE proofread_candidates (
            id TEXT PRIMARY KEY,
            run_id TEXT NOT NULL REFERENCES proofread_runs(id) ON DELETE CASCADE,
            kind TEXT NOT NULL,
            location_json TEXT NOT NULL,
            expected_original TEXT NOT NULL DEFAULT '',
            category TEXT NOT NULL,
            first_confidence TEXT NOT NULL,
            first_replacement TEXT NOT NULL DEFAULT '',
            verification TEXT NOT NULL,
            verified_replacement TEXT NOT NULL DEFAULT '',
            reason TEXT NOT NULL DEFAULT '',
            created_at TEXT NOT NULL
        )`,
		`CREATE INDEX proofread_candidates_run_created ON proofread_candidates(run_id, created_at)`,
		`CREATE TABLE candidate_decisions (
            id TEXT PRIMARY KEY,
            candidate_id TEXT NOT NULL REFERENCES proofread_candidates(id) ON DELETE CASCADE,
            decision TEXT NOT NULL CHECK (decision IN ('accept', 'reject', 'modify')),
            replacement TEXT NOT NULL DEFAULT '',
            created_at TEXT NOT NULL
        )`,
		`CREATE INDEX candidate_decisions_candidate_created ON candidate_decisions(candidate_id, created_at DESC)`,
		`CREATE TABLE revision_candidates (
            revision_file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
            candidate_id TEXT NOT NULL REFERENCES proofread_candidates(id),
            outcome TEXT NOT NULL,
            replacement TEXT NOT NULL DEFAULT '',
            PRIMARY KEY(revision_file_id, candidate_id)
        )`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema version 1: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	return tx.Commit()
}
