package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	maxFileSize      = 200 << 20
	createdLifetime  = 10 * time.Minute
	offlineLifetime  = 2 * time.Hour
	managementTokenN = 32
	objectIDN        = 24
	sessionCodeTries = 100
)

type transferMode string
type transferState string

const (
	modeOnline  transferMode = "ONLINE"
	modeR2      transferMode = "R2"
	modeServerA transferMode = "SERVER_A"

	stateCreated   transferState = "CREATED"
	stateUploading transferState = "UPLOADING"
	stateReady     transferState = "READY"
	stateFailed    transferState = "FAILED"
	stateRevoked   transferState = "REVOKED"
	stateExpired   transferState = "EXPIRED"
)

type transferSession struct {
	ID                   int64         `json:"-"`
	Code                 string        `json:"code"`
	Mode                 transferMode  `json:"mode"`
	State                transferState `json:"state"`
	Filename             string        `json:"filename"`
	ContentType          string        `json:"content_type"`
	ExpectedSize         int64         `json:"expected_size"`
	SHA256Hex            string        `json:"sha256"`
	StorageObjectID      string        `json:"-"`
	StorageETag          string        `json:"etag,omitempty"`
	CreatedAt            time.Time     `json:"created_at"`
	FirstUploadStartedAt *time.Time    `json:"first_upload_started_at,omitempty"`
	ExpiresAt            time.Time     `json:"expires_at"`
	RevokedAt            *time.Time    `json:"revoked_at,omitempty"`
}

type sessionStore struct {
	db  *sql.DB
	now func() time.Time
}

func openSessionStore(path string) (*sessionStore, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open control database: %w", err)
	}
	s := &sessionStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *sessionStore) Close() error { return s.db.Close() }

func (s *sessionStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS transfer_sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	code TEXT NOT NULL,
	mode TEXT NOT NULL CHECK(mode IN ('ONLINE','R2','SERVER_A')),
	state TEXT NOT NULL CHECK(state IN ('CREATED','UPLOADING','READY','FAILED','REVOKED','EXPIRED')),
	filename TEXT NOT NULL,
	content_type TEXT NOT NULL,
	expected_size INTEGER NOT NULL,
	sha256_hex TEXT NOT NULL,
	storage_object_id TEXT NOT NULL UNIQUE,
	storage_etag TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	first_upload_started_at INTEGER,
	expires_at INTEGER NOT NULL,
	revoked_at INTEGER,
	management_token_hash BLOB NOT NULL,
	storage_deleted_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_transfer_expiry ON transfer_sessions(expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_active_transfer_code
	ON transfer_sessions(code)
	WHERE state IN ('CREATED','UPLOADING','READY');
`)
	if err != nil {
		return fmt.Errorf("migrate control database: %w", err)
	}
	// Existing development databases created before cleanup tracking are
	// upgraded in place. SQLite has no ADD COLUMN IF NOT EXISTS.
	_, alterErr := s.db.Exec(`ALTER TABLE transfer_sessions ADD COLUMN storage_deleted_at INTEGER`)
	if alterErr != nil && !strings.Contains(strings.ToLower(alterErr.Error()), "duplicate column") {
		return fmt.Errorf("add storage cleanup marker: %w", alterErr)
	}
	return nil
}

func (s *sessionStore) create(mode transferMode, filename, contentType string, size int64, sha string) (transferSession, string, error) {
	if mode != modeOnline && mode != modeR2 && mode != modeServerA {
		return transferSession{}, "", errors.New("invalid transfer mode")
	}
	filename = strings.TrimSpace(filename)
	if filename == "" || len(filename) > 255 || strings.ContainsAny(filename, "\r\n") {
		return transferSession{}, "", errors.New("invalid filename")
	}
	if size < 0 || size > maxFileSize {
		return transferSession{}, "", errors.New("invalid file size")
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if mode != modeOnline && !validSHA256(sha) {
		return transferSession{}, "", errors.New("invalid SHA-256")
	}
	token, err := randomHex(managementTokenN)
	if err != nil {
		return transferSession{}, "", err
	}
	objectID, err := randomHex(objectIDN)
	if err != nil {
		return transferSession{}, "", err
	}
	tokenHash := sha256.Sum256([]byte(token))
	now := s.currentTime().UTC()
	for i := 0; i < sessionCodeTries; i++ {
		code, err := randomID(6)
		if err != nil {
			return transferSession{}, "", err
		}
		result, err := s.db.Exec(`
INSERT INTO transfer_sessions
	(code, mode, state, filename, content_type, expected_size, sha256_hex,
	 storage_object_id, created_at, expires_at, management_token_hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			code, mode, stateCreated, filename, contentType, size, sha,
			objectID, now.Unix(), now.Add(createdLifetime).Unix(), tokenHash[:])
		if isUniqueViolation(err) {
			continue
		}
		if err != nil {
			return transferSession{}, "", fmt.Errorf("create transfer: %w", err)
		}
		id, _ := result.LastInsertId()
		return transferSession{
			ID: id, Code: code, Mode: mode, State: stateCreated,
			Filename: filename, ContentType: contentType, ExpectedSize: size,
			SHA256Hex: sha, StorageObjectID: objectID, CreatedAt: now,
			ExpiresAt: now.Add(createdLifetime),
		}, token, nil
	}
	return transferSession{}, "", errors.New("could not allocate an active six-digit code")
}

func (s *sessionStore) markUploadStarted(code, token string, lifetime time.Duration) (transferSession, error) {
	if lifetime <= 0 {
		lifetime = offlineLifetime
	}
	if err := s.expireDue(); err != nil {
		return transferSession{}, err
	}
	now := s.currentTime().UTC()
	hash := sha256.Sum256([]byte(token))
	result, err := s.db.Exec(`
UPDATE transfer_sessions
SET state='UPLOADING', first_upload_started_at=?, expires_at=?
WHERE code=? AND management_token_hash=? AND state='CREATED'`,
		now.Unix(), now.Add(lifetime).Unix(), code, hash[:])
	if err != nil {
		return transferSession{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		// Idempotency: an already-started upload is returned without extending it.
		var session transferSession
		session, err = s.getManaged(code, token)
		if err != nil {
			return transferSession{}, err
		}
		if session.State != stateUploading && session.State != stateReady {
			return transferSession{}, fmt.Errorf("transfer cannot start from %s", session.State)
		}
		return session, nil
	}
	return s.getManaged(code, token)
}

func (s *sessionStore) markReady(code, token, etag string) (transferSession, error) {
	hash := sha256.Sum256([]byte(token))
	result, err := s.db.Exec(`
UPDATE transfer_sessions SET state='READY', storage_etag=?
WHERE code=? AND management_token_hash=? AND state='UPLOADING'`,
		strings.TrimSpace(etag), code, hash[:])
	if err != nil {
		return transferSession{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		current, getErr := s.getManaged(code, token)
		if getErr != nil {
			return transferSession{}, getErr
		}
		if current.State != stateReady {
			return transferSession{}, fmt.Errorf("transfer cannot become ready from %s", current.State)
		}
		return current, nil
	}
	return s.getManaged(code, token)
}

func (s *sessionStore) revoke(code, token string) (transferSession, error) {
	hash := sha256.Sum256([]byte(token))
	now := s.currentTime().UTC()
	result, err := s.db.Exec(`
UPDATE transfer_sessions SET state='REVOKED', revoked_at=?
WHERE code=? AND management_token_hash=? AND state IN ('CREATED','UPLOADING','READY','FAILED')`,
		now.Unix(), code, hash[:])
	if err != nil {
		return transferSession{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return transferSession{}, errors.New("transfer not found, expired, or already revoked")
	}
	return s.getManaged(code, token)
}

func (s *sessionStore) getManaged(code, token string) (transferSession, error) {
	hash := sha256.Sum256([]byte(token))
	return s.scanOne(`
SELECT id, code, mode, state, filename, content_type, expected_size, sha256_hex,
       storage_object_id, storage_etag, created_at, first_upload_started_at,
       expires_at, revoked_at
FROM transfer_sessions WHERE code=? AND management_token_hash=?`, code, hash[:])
}

func (s *sessionStore) getReady(code string) (transferSession, error) {
	if err := s.expireDue(); err != nil {
		return transferSession{}, err
	}
	return s.scanOne(`
SELECT id, code, mode, state, filename, content_type, expected_size, sha256_hex,
       storage_object_id, storage_etag, created_at, first_upload_started_at,
       expires_at, revoked_at
FROM transfer_sessions WHERE code=? AND state='READY'`, code)
}

func (s *sessionStore) scanOne(query string, args ...any) (transferSession, error) {
	var out transferSession
	var mode, state string
	var created, expires int64
	var started, revoked sql.NullInt64
	err := s.db.QueryRow(query, args...).Scan(
		&out.ID, &out.Code, &mode, &state, &out.Filename, &out.ContentType,
		&out.ExpectedSize, &out.SHA256Hex, &out.StorageObjectID, &out.StorageETag,
		&created, &started, &expires, &revoked,
	)
	if err != nil {
		return transferSession{}, err
	}
	out.Mode, out.State = transferMode(mode), transferState(state)
	out.CreatedAt, out.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(expires, 0).UTC()
	if started.Valid {
		value := time.Unix(started.Int64, 0).UTC()
		out.FirstUploadStartedAt = &value
	}
	if revoked.Valid {
		value := time.Unix(revoked.Int64, 0).UTC()
		out.RevokedAt = &value
	}
	return out, nil
}

func (s *sessionStore) expireDue() error {
	now := s.currentTime().UTC().Unix()
	_, err := s.db.Exec(`
UPDATE transfer_sessions SET state='EXPIRED'
WHERE state IN ('CREATED','UPLOADING','READY','FAILED') AND expires_at<=?`, now)
	return err
}

func (s *sessionStore) pendingExpiredObjects() ([]transferSession, error) {
	if err := s.expireDue(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
SELECT id, code, mode, state, filename, content_type, expected_size, sha256_hex,
       storage_object_id, storage_etag, created_at, first_upload_started_at,
       expires_at, revoked_at
FROM transfer_sessions
WHERE state IN ('EXPIRED','REVOKED') AND storage_deleted_at IS NULL AND mode IN ('R2','SERVER_A')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []transferSession
	for rows.Next() {
		var session transferSession
		var mode, state string
		var created, expires int64
		var started, revoked sql.NullInt64
		if err := rows.Scan(
			&session.ID, &session.Code, &mode, &state, &session.Filename, &session.ContentType,
			&session.ExpectedSize, &session.SHA256Hex, &session.StorageObjectID,
			&session.StorageETag, &created, &started, &expires, &revoked,
		); err != nil {
			return nil, err
		}
		session.Mode, session.State = transferMode(mode), transferState(state)
		session.CreatedAt, session.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(expires, 0).UTC()
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *sessionStore) markStorageDeleted(id int64) error {
	_, err := s.db.Exec(`UPDATE transfer_sessions SET storage_deleted_at=? WHERE id=?`,
		s.currentTime().UTC().Unix(), id)
	return err
}

func (s *sessionStore) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func randomHex(bytesN int) (string, error) {
	value := make([]byte, bytesN)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
