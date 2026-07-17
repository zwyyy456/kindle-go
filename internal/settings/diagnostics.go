package settings

import "os/exec"

type Diagnostics struct {
	PythonPath string
	CodexPath  string
}

func BasicDiagnostics() Diagnostics {
	python, _ := exec.LookPath("python3")
	codex, _ := exec.LookPath("codex")
	return Diagnostics{PythonPath: python, CodexPath: codex}
}
