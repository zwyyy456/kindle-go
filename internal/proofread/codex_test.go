package proofread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexClientUsesIsolatedStructuredInvocation(t *testing.T) {
	work := t.TempDir()
	fake, argsFile, stdinFile := fakeCodex(t, work)
	t.Setenv("FAKE_CODEX_MODE", "success")
	t.Setenv("FAKE_CODEX_RESULT", `{"answer":"ok"}`)
	client := CodexClient{Path: fake, Model: "test-model", Runner: OSCommandRunner{}}
	var output struct {
		Answer string `json:"answer"`
	}
	err := client.Invoke(context.Background(), CodexRequest{
		WorkDir: work, Prompt: []byte("只返回结构化结果"), Schema: []byte(`{"type":"object"}`),
		Images: []string{"one.png", "two.jpg"},
	}, &output)
	if err != nil || output.Answer != "ok" {
		t.Fatalf("invoke = %#v, %v", output, err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argumentText := string(args)
	for _, expected := range []string{
		"exec\n", "--ephemeral\n", "--ignore-user-config\n", "--ignore-rules\n", "--skip-git-repo-check\n",
		"--sandbox\nread-only\n", "-C\n" + work + "\n", "--model\ntest-model\n",
		"--image\none.png\n", "--image\ntwo.jpg\n", "--output-schema\n", "--output-last-message\n", "-\n",
	} {
		if !strings.Contains(argumentText, expected) {
			t.Errorf("arguments missing %q in %q", expected, argumentText)
		}
	}
	prompt, err := os.ReadFile(stdinFile)
	if err != nil || string(prompt) != "只返回结构化结果" {
		t.Fatalf("stdin = %q, %v", prompt, err)
	}
	if leftovers, err := filepath.Glob(filepath.Join(work, ".codex-*.json")); err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary Codex files = %#v, %v", leftovers, err)
	}
}

func TestCodexClientRejectsUnknownFieldsAndMultipleJSONValues(t *testing.T) {
	for name, result := range map[string]string{
		"unknown":  `{"answer":"ok","extra":true}`,
		"multiple": `{"answer":"ok"} {"answer":"again"}`,
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			fake, _, _ := fakeCodex(t, work)
			t.Setenv("FAKE_CODEX_MODE", "success")
			t.Setenv("FAKE_CODEX_RESULT", result)
			var output struct {
				Answer string `json:"answer"`
			}
			err := (CodexClient{Path: fake}).Invoke(context.Background(), CodexRequest{WorkDir: work, Prompt: []byte("prompt"), Schema: []byte(`{"type":"object"}`)}, &output)
			var codexErr *CodexError
			if !errors.As(err, &codexErr) || codexErr.Code != "codex_invalid_output" {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestCodexClientMapsFailureAndBoundsStderr(t *testing.T) {
	work := t.TempDir()
	fake, _, _ := fakeCodex(t, work)
	t.Setenv("FAKE_CODEX_MODE", "fail")
	var output map[string]any
	err := (CodexClient{Path: fake}).Invoke(context.Background(), CodexRequest{WorkDir: work, Prompt: []byte("private book text"), Schema: []byte(`{"type":"object"}`)}, &output)
	var codexErr *CodexError
	if !errors.As(err, &codexErr) || codexErr.Code != "codex_unavailable" || codexErr.ExitCode != 7 || len(codexErr.Stderr) > defaultCapturedOutputBytes {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "private book text") {
		t.Fatalf("error leaked prompt: %v", err)
	}
}

func TestCodexClientHonorsTimeoutAndMissingExecutable(t *testing.T) {
	work := t.TempDir()
	fake, _, _ := fakeCodex(t, work)
	t.Setenv("FAKE_CODEX_MODE", "wait")
	request := CodexRequest{WorkDir: work, Schema: []byte(`{"type":"object"}`)}
	err := (CodexClient{Path: fake, Timeout: 50 * time.Millisecond}).Invoke(context.Background(), request, &map[string]any{})
	var codexErr *CodexError
	if !errors.As(err, &codexErr) || codexErr.Code != "codex_unavailable" || !strings.Contains(codexErr.Message, "timed out") {
		t.Fatalf("timeout error = %#v", err)
	}
	err = (CodexClient{Path: filepath.Join(work, "missing-codex")}).Invoke(context.Background(), request, &map[string]any{})
	if !errors.As(err, &codexErr) || codexErr.Code != "codex_not_found" {
		t.Fatalf("missing error = %#v", err)
	}
}

func fakeCodex(t *testing.T, root string) (string, string, string) {
	t.Helper()
	path := filepath.Join(root, "fake-codex")
	argsFile := filepath.Join(root, "args.txt")
	stdinFile := filepath.Join(root, "stdin.txt")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$FAKE_CODEX_ARGS"
cat > "$FAKE_CODEX_STDIN"
result=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--output-last-message' ]; then result="$2"; shift 2; else shift; fi
done
case "${FAKE_CODEX_MODE:-success}" in
  success) printf '%s' "$FAKE_CODEX_RESULT" > "$result" ;;
  fail) i=0; while [ "$i" -lt 70000 ]; do printf x >&2; i=$((i + 1)); done; exit 7 ;;
  wait) while :; do :; done ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX_ARGS", argsFile)
	t.Setenv("FAKE_CODEX_STDIN", stdinFile)
	return path, argsFile, stdinFile
}
