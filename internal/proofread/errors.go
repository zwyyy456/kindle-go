package proofread

import (
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func sourceResolutionError(err error) error {
	if code := library.SourceErrorCode(err); code != "" {
		return &task.ExecutionError{Code: code, Err: err}
	}
	return err
}
