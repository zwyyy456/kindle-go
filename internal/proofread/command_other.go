//go:build !unix

package proofread

import "os/exec"

func configureCommandCancellation(_ *exec.Cmd) {}
