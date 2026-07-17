package proofread

import (
	"context"
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
