package longepubproofreader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseWritesVersionedEPUBEngine(t *testing.T) {
	base := t.TempDir()
	first, err := Release(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Version) != 64 || !strings.Contains(first.Root, first.Version[:16]) {
		t.Fatalf("runtime = %#v", first)
	}
	for _, filename := range []string{first.MainScript, first.ApplyScript, filepath.Join(first.Root, "scripts", "epub_core.py")} {
		if info, err := os.Stat(filename); err != nil || info.Size() == 0 {
			t.Fatalf("released file %q = %#v, %v", filename, info, err)
		}
	}
	second, err := Release(base)
	if err != nil || second.Root != first.Root {
		t.Fatalf("second release = %#v, %v", second, err)
	}
}
