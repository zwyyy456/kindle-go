package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelpDescribesTransferRoles(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"control", "storage", "r2-probe"} {
		if !strings.Contains(stdout.String(), role) {
			t.Fatalf("help omitted %q:\n%s", role, stdout.String())
		}
	}
}

func TestRoleHelpDoesNotStartServers(t *testing.T) {
	for _, role := range []string{"control", "storage", "r2-probe"} {
		t.Run(role, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run([]string{role, "-h"}, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() == 0 {
				t.Fatal("flag help was empty")
			}
		})
	}
}
