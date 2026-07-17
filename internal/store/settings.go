package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) Setting(ctx context.Context, key string) (string, time.Time, bool, error) {
	var value, updated string
	err := s.db.QueryRowContext(ctx, `SELECT value_json, updated_at FROM settings WHERE key = ?`, key).Scan(&value, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	updatedAt, err := parseTime(updated)
	return value, updatedAt, err == nil, err
}

func (s *Store) SaveSetting(ctx context.Context, key, value string, updatedAt time.Time) error {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value_json, updated_at) VALUES(?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`, key, value, formatTime(updatedAt))
	return err
}

func (s *Store) SeedSetting(ctx context.Context, key, value string, updatedAt time.Time) error {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value_json, updated_at) VALUES(?, ?, ?)
ON CONFLICT(key) DO NOTHING`, key, value, formatTime(updatedAt))
	return err
}
