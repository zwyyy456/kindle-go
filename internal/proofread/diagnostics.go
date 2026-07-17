package proofread

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var requiredCodexFlags = []string{"--ephemeral", "--ignore-user-config", "--ignore-rules", "--image", "--output-schema", "--output-last-message"}

type DependencyDiagnostics struct {
	PythonPath, PythonVersion string
	CodexPath, CodexVersion   string
	CodexFlags                map[string]bool
}

func CheckCodexLogin(ctx context.Context, runner CommandRunner, codexPath string) (string, error) {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	path := strings.TrimSpace(codexPath)
	if path == "" {
		path, _ = exec.LookPath("codex")
	}
	if path == "" {
		return "", &CodexError{Code: "codex_not_found", Message: "Codex CLI was not found in PATH"}
	}
	result, err := runner.Run(ctx, CommandSpec{Path: path, Args: []string{"login", "status"}, Timeout: 15 * time.Second})
	if err != nil {
		return "", &CodexError{Code: "codex_unavailable", Message: "Codex login status check failed", Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
	status := strings.TrimSpace(firstNonEmpty(result.Stdout, result.Stderr))
	if status == "" {
		return "", fmt.Errorf("Codex login status returned no result")
	}
	return status, nil
}

func (d DependencyDiagnostics) CodexFlagsReady() bool {
	if len(d.CodexFlags) != len(requiredCodexFlags) {
		return false
	}
	for _, flag := range requiredCodexFlags {
		if !d.CodexFlags[flag] {
			return false
		}
	}
	return true
}

func DiagnoseDependencies(ctx context.Context, runner CommandRunner, pythonPath, codexPath string) DependencyDiagnostics {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	result := DependencyDiagnostics{CodexFlags: map[string]bool{}}
	result.PythonPath, result.PythonVersion = diagnoseVersion(ctx, runner, pythonPath, "python3", []string{"--version"})
	result.CodexPath, result.CodexVersion = diagnoseVersion(ctx, runner, codexPath, "codex", []string{"--version"})
	if result.CodexPath != "" {
		help, _ := runner.Run(ctx, CommandSpec{Path: result.CodexPath, Args: []string{"exec", "--help"}, Timeout: 10 * time.Second})
		text := help.Stdout + help.Stderr
		for _, flag := range requiredCodexFlags {
			result.CodexFlags[flag] = strings.Contains(text, flag)
		}
	}
	return result
}

func diagnoseVersion(ctx context.Context, runner CommandRunner, configured, fallback string, args []string) (string, string) {
	path := strings.TrimSpace(configured)
	if path == "" {
		path, _ = exec.LookPath(fallback)
	}
	if path == "" {
		return "", ""
	}
	result, err := runner.Run(ctx, CommandSpec{Path: path, Args: args, Timeout: 10 * time.Second})
	if err != nil {
		return path, ""
	}
	return path, strings.TrimSpace(firstNonEmpty(result.Stdout, result.Stderr))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
