package longnovelproofreader

import (
	"embed"
	"sync"

	"github.com/flashdict/kindle2flashdict/internal/enginebundle"
)

//go:embed scripts references
var engineFiles embed.FS

type Runtime = enginebundle.Runtime

var versionOnce sync.Once
var version string

func Version() string {
	versionOnce.Do(func() { version = enginebundle.Version(engineFiles) })
	return version
}

func Release(baseDir string) (Runtime, error) {
	return enginebundle.ReleaseVersion(engineFiles, baseDir, "long-novel-proofreader", Version(), "scripts/proofread_txt.py", "scripts/apply_reviewed_edits.py")
}
