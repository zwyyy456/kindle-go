package proofread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultCodexTimeout = 20 * time.Minute

type CodexClient struct {
	Path, Model string
	Timeout     time.Duration
	Runner      CommandRunner
}

type CodexRequest struct {
	WorkDir string
	Prompt  []byte
	Schema  []byte
	Images  []string
}

type CodexError struct {
	Code, Message, Stderr string
	ExitCode              int
}

func (e *CodexError) Error() string { return e.Code + ": " + e.Message }

func (c CodexClient) Invoke(ctx context.Context, request CodexRequest, output any) error {
	if output == nil {
		return &CodexError{Code: "codex_invalid_output", Message: "output destination is required"}
	}
	if !json.Valid(request.Schema) {
		return &CodexError{Code: "codex_invalid_output", Message: "output schema is not valid JSON"}
	}
	if info, err := os.Stat(request.WorkDir); err != nil || !info.IsDir() {
		return &CodexError{Code: "codex_unavailable", Message: "Codex work directory is unavailable"}
	}
	schemaFile, err := os.CreateTemp(request.WorkDir, ".codex-schema-*.json")
	if err != nil {
		return err
	}
	schemaPath := schemaFile.Name()
	defer os.Remove(schemaPath)
	if _, err := schemaFile.Write(request.Schema); err != nil {
		schemaFile.Close()
		return err
	}
	if err := schemaFile.Close(); err != nil {
		return err
	}
	resultFile, err := os.CreateTemp(request.WorkDir, ".codex-result-*.json")
	if err != nil {
		return err
	}
	resultPath := resultFile.Name()
	if err := resultFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(resultPath); err != nil {
		return err
	}
	defer os.Remove(resultPath)

	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = "codex"
	}
	args := []string{"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check", "--sandbox", "read-only", "-C", request.WorkDir}
	if model := strings.TrimSpace(c.Model); model != "" {
		args = append(args, "--model", model)
	}
	for _, image := range request.Images {
		if strings.TrimSpace(image) != "" {
			args = append(args, "--image", image)
		}
	}
	args = append(args, "--output-schema", schemaPath, "--output-last-message", resultPath, "-")
	runner := c.Runner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultCodexTimeout
	}
	result, runErr := runner.Run(ctx, CommandSpec{Path: path, Dir: request.WorkDir, Args: args, Stdin: request.Prompt, Timeout: timeout})
	if runErr != nil {
		return codexRunError(runErr, result)
	}
	data, err := os.ReadFile(filepath.Clean(resultPath))
	if err != nil {
		return &CodexError{Code: "codex_invalid_output", Message: "Codex did not write a result file", Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return &CodexError{Code: "codex_invalid_output", Message: fmt.Sprintf("decode Codex result: %v", err), Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
	if err := requireJSONEOF(decoder); err != nil {
		return &CodexError{Code: "codex_invalid_output", Message: err.Error(), Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
	return nil
}

func codexRunError(err error, result CommandResult) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &CodexError{Code: "codex_unavailable", Message: "Codex invocation timed out", Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
	if errors.Is(err, context.Canceled) {
		return &CodexError{Code: "codex_unavailable", Message: "Codex invocation was canceled", Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) || errors.Is(err, os.ErrNotExist) {
		return &CodexError{Code: "codex_not_found", Message: "Codex CLI was not found", Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		return &CodexError{Code: "codex_unavailable", Message: fmt.Sprintf("Codex CLI exited with status %d", commandErr.ExitCode), Stderr: commandErr.Stderr, ExitCode: commandErr.ExitCode}
	}
	return &CodexError{Code: "codex_unavailable", Message: err.Error(), Stderr: result.Stderr, ExitCode: result.ExitCode}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Codex result contains more than one JSON value")
		}
		return fmt.Errorf("decode trailing Codex result: %w", err)
	}
	return nil
}
