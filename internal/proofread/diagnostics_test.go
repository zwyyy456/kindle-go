package proofread

import (
	"context"
	"strings"
	"testing"
)

type runnerFunc func(context.Context, CommandSpec) (CommandResult, error)

func (f runnerFunc) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	return f(ctx, spec)
}

func TestDiagnoseDependenciesChecksRequiredCodexFlags(t *testing.T) {
	runner := runnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
		joined := strings.Join(spec.Args, " ")
		switch {
		case strings.Contains(joined, "exec --help"):
			return CommandResult{Stdout: strings.Join(requiredCodexFlags, " ")}, nil
		case spec.Path == "/python":
			return CommandResult{Stderr: "Python 3.13.0\n"}, nil
		default:
			return CommandResult{Stdout: "codex-cli 1.0\n"}, nil
		}
	})
	result := DiagnoseDependencies(context.Background(), runner, "/python", "/codex")
	if result.PythonVersion != "Python 3.13.0" || result.CodexVersion != "codex-cli 1.0" {
		t.Fatalf("versions = %#v", result)
	}
	for _, flag := range requiredCodexFlags {
		if !result.CodexFlags[flag] {
			t.Errorf("missing flag %s in %#v", flag, result.CodexFlags)
		}
	}
}

func TestCheckCodexLoginUsesStatusWithoutModelInvocation(t *testing.T) {
	var received CommandSpec
	runner := runnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
		received = spec
		return CommandResult{Stdout: "Logged in using ChatGPT\n"}, nil
	})
	status, err := CheckCodexLogin(context.Background(), runner, "/codex")
	if err != nil || status != "Logged in using ChatGPT" {
		t.Fatalf("status = %q, %v", status, err)
	}
	if received.Path != "/codex" || strings.Join(received.Args, " ") != "login status" {
		t.Fatalf("command = %#v", received)
	}
}
