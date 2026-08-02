package library

import (
	"errors"
	"fmt"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

var (
	ErrSourceChanged     = errors.New("source changed")
	ErrSourceUnavailable = errors.New("source unavailable")
)

func SourceErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrSourceChanged):
		return "source_hash_mismatch"
	case errors.Is(err, ErrSourceUnavailable):
		return "source_unavailable"
	default:
		return ""
	}
}

func wrapSourceUnavailable(err error) error {
	if err == nil {
		return ErrSourceUnavailable
	}
	return fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
}

type DeletionError struct {
	Code    string
	Message string
}

func (e *DeletionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func mapDeletionError(err error) error {
	if err == nil {
		return nil
	}
	var active *store.ActiveTasksError
	if errors.As(err, &active) {
		return &DeletionError{
			Code:    "book_has_active_tasks",
			Message: fmt.Sprintf("book has %d active task(s); cancel them before deleting the book", active.Count),
		}
	}
	var referenced *store.ReferencedFileError
	if errors.As(err, &referenced) {
		return &DeletionError{
			Code:    "file_has_references",
			Message: fmt.Sprintf("file is used as the source of %d ready or pending file(s) and cannot be deleted", referenced.Count),
		}
	}
	var protected *store.ProtectedFileError
	if errors.As(err, &protected) {
		return &DeletionError{Code: "protected_file", Message: "original files cannot be deleted separately"}
	}
	return err
}
