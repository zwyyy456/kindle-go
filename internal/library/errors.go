package library

import (
	"errors"
	"fmt"
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
