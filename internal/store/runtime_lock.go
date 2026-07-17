package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type RuntimeLock struct {
	InstanceID  string
	PID         int
	HeartbeatAt time.Time
}

type RuntimeLockHeldError struct {
	Lock RuntimeLock
}

func (e *RuntimeLockHeldError) Error() string {
	return fmt.Sprintf("library worker is already running (instance %s, pid %d)", e.Lock.InstanceID, e.Lock.PID)
}

func (s *Store) ClaimRuntimeLock(ctx context.Context, instanceID string, pid int, now time.Time, staleAfter time.Duration) error {
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("runtime lock instance ID is required")
	}
	if pid <= 0 {
		return errors.New("runtime lock PID must be positive")
	}
	if staleAfter <= 0 {
		return errors.New("runtime lock stale duration must be positive")
	}
	if now.IsZero() {
		now = time.Now()
	}

	result, err := s.db.ExecContext(ctx, `
INSERT INTO runtime_lock(singleton, instance_id, pid, heartbeat_at)
VALUES(1, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
    instance_id = excluded.instance_id,
    pid = excluded.pid,
    heartbeat_at = excluded.heartbeat_at
WHERE runtime_lock.instance_id = excluded.instance_id
   OR julianday(runtime_lock.heartbeat_at) <= julianday(?)
`, instanceID, pid, formatTime(now), formatTime(now.Add(-staleAfter)))
	if err != nil {
		return fmt.Errorf("claim runtime lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 0 {
		return nil
	}
	lock, ok, err := s.RuntimeLock(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("runtime lock changed while claiming; retry")
	}
	return &RuntimeLockHeldError{Lock: lock}
}

func (s *Store) HeartbeatRuntimeLock(ctx context.Context, instanceID string, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE runtime_lock SET heartbeat_at = ? WHERE singleton = 1 AND instance_id = ?`, formatTime(now), instanceID)
	if err != nil {
		return fmt.Errorf("heartbeat runtime lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("runtime lock is not owned by instance %q", instanceID)
	}
	return nil
}

func (s *Store) ReleaseRuntimeLock(ctx context.Context, instanceID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM runtime_lock WHERE singleton = 1 AND instance_id = ?`, instanceID)
	if err != nil {
		return fmt.Errorf("release runtime lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("runtime lock is not owned by instance %q", instanceID)
	}
	return nil
}

func (s *Store) RuntimeLock(ctx context.Context) (RuntimeLock, bool, error) {
	var lock RuntimeLock
	var heartbeat string
	err := s.db.QueryRowContext(ctx, `SELECT instance_id, pid, heartbeat_at FROM runtime_lock WHERE singleton = 1`).Scan(&lock.InstanceID, &lock.PID, &heartbeat)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeLock{}, false, nil
	}
	if err != nil {
		return RuntimeLock{}, false, err
	}
	lock.HeartbeatAt, err = parseTime(heartbeat)
	if err != nil {
		return RuntimeLock{}, false, err
	}
	return lock, true, nil
}
