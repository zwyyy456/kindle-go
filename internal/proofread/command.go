package proofread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultCapturedOutputBytes = 64 << 10

type CommandSpec struct {
	Path, Dir string
	Args      []string
	Env       []string
	Stdin     []byte
	Timeout   time.Duration
}

type CommandResult struct {
	Stdout, Stderr string
	ExitCode       int
	Duration       time.Duration
}

type CommandRunner interface {
	Run(context.Context, CommandSpec) (CommandResult, error)
}

func commandNotFound(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) || errors.Is(err, os.ErrNotExist)
}

type OSCommandRunner struct {
	CapturedOutputBytes int
}

type CommandError struct {
	Path, Stderr string
	ExitCode     int
}

func (e *CommandError) Error() string {
	message := fmt.Sprintf("command %q exited with status %d", e.Path, e.ExitCode)
	if strings.TrimSpace(e.Stderr) != "" {
		message += ": " + strings.TrimSpace(e.Stderr)
	}
	return message
}

func (r OSCommandRunner) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	if strings.TrimSpace(spec.Path) == "" {
		return CommandResult{ExitCode: -1}, errors.New("command path is required")
	}
	runCtx := ctx
	cancel := func() {}
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	defer cancel()
	command := exec.CommandContext(runCtx, spec.Path, spec.Args...)
	configureCommandCancellation(command)
	command.Dir = spec.Dir
	if len(spec.Env) != 0 {
		command.Env = append(os.Environ(), spec.Env...)
	}
	command.Stdin = bytes.NewReader(spec.Stdin)
	limit := r.CapturedOutputBytes
	if limit <= 0 {
		limit = defaultCapturedOutputBytes
	}
	stdout, stderr := newTailWriter(limit), newTailWriter(limit)
	command.Stdout, command.Stderr = stdout, stderr
	started := time.Now()
	err := command.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1, Duration: time.Since(started)}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, &CommandError{Path: spec.Path, ExitCode: result.ExitCode, Stderr: result.Stderr}
		}
		return result, err
	}
	return result, nil
}

type tailWriter struct {
	limit int
	data  []byte
}

func newTailWriter(limit int) *tailWriter { return &tailWriter{limit: limit} }

func (w *tailWriter) Write(data []byte) (int, error) {
	written := len(data)
	if len(data) >= w.limit {
		w.data = append(w.data[:0], data[len(data)-w.limit:]...)
		return written, nil
	}
	if overflow := len(w.data) + len(data) - w.limit; overflow > 0 {
		copy(w.data, w.data[overflow:])
		w.data = w.data[:len(w.data)-overflow]
	}
	w.data = append(w.data, data...)
	return written, nil
}

func (w *tailWriter) String() string {
	return string(w.data)
}
