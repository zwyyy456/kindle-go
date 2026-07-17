package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type CompatibilityRecord struct {
	ID, FileID, SourceSHA256, Status                            string
	MetadataJSON, SpineJSON, TOCJSON, ResourcesJSON, IssuesJSON string
	CheckedAt                                                   time.Time
}

func (s *Store) SaveCompatibility(ctx context.Context, record CompatibilityRecord) error {
	if record.ID == "" {
		var err error
		record.ID, err = NewID()
		if err != nil {
			return err
		}
	}
	if record.CheckedAt.IsZero() {
		record.CheckedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO compatibility_reports(id, file_id, source_sha256, status, metadata_json, spine_json, toc_json, resources_json, issues_json, checked_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(file_id) DO UPDATE SET source_sha256=excluded.source_sha256, status=excluded.status, metadata_json=excluded.metadata_json, spine_json=excluded.spine_json, toc_json=excluded.toc_json, resources_json=excluded.resources_json, issues_json=excluded.issues_json, checked_at=excluded.checked_at`, record.ID, record.FileID, record.SourceSHA256, record.Status, record.MetadataJSON, record.SpineJSON, record.TOCJSON, record.ResourcesJSON, record.IssuesJSON, formatTime(record.CheckedAt))
	return err
}

func (s *Store) Compatibility(ctx context.Context, fileID string) (CompatibilityRecord, bool, error) {
	var record CompatibilityRecord
	var checked string
	err := s.db.QueryRowContext(ctx, `SELECT id, file_id, source_sha256, status, metadata_json, spine_json, toc_json, resources_json, issues_json, checked_at FROM compatibility_reports WHERE file_id = ?`, fileID).Scan(&record.ID, &record.FileID, &record.SourceSHA256, &record.Status, &record.MetadataJSON, &record.SpineJSON, &record.TOCJSON, &record.ResourcesJSON, &record.IssuesJSON, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return CompatibilityRecord{}, false, nil
	}
	if err != nil {
		return CompatibilityRecord{}, false, err
	}
	record.CheckedAt, err = parseTime(checked)
	return record, err == nil, err
}
