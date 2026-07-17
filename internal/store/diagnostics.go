package store

import (
	"context"
	"os"
	"syscall"
)

type Diagnostics struct {
	SchemaVersion                       int
	JournalMode                         string
	LibraryWritable                     bool
	FreeBytes                           uint64
	QueuedGeneration, RunningGeneration int
	QueuedProofread, RunningProofread   int
}

func (s *Store) Diagnostics(ctx context.Context) (Diagnostics, error) {
	var result Diagnostics
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&result.SchemaVersion); err != nil {
		return Diagnostics{}, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&result.JournalMode); err != nil {
		return Diagnostics{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT
COALESCE(SUM(CASE WHEN status = 'queued' AND type != 'proofread' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN status = 'running' AND type != 'proofread' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN status = 'queued' AND type = 'proofread' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN status = 'running' AND type = 'proofread' THEN 1 ELSE 0 END), 0)
FROM tasks`).Scan(&result.QueuedGeneration, &result.RunningGeneration, &result.QueuedProofread, &result.RunningProofread); err != nil {
		return Diagnostics{}, err
	}
	temporary, err := os.CreateTemp(s.root, ".write-check-")
	if err == nil {
		result.LibraryWritable = true
		name := temporary.Name()
		_ = temporary.Close()
		_ = os.Remove(name)
	}
	var stats syscall.Statfs_t
	if err := syscall.Statfs(s.root, &stats); err == nil {
		result.FreeBytes = stats.Bavail * uint64(stats.Bsize)
	}
	return result, nil
}
