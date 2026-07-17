package enginebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Runtime struct {
	Version, Root, MainScript, ApplyScript string
}

func Version(files fs.FS) string {
	hash := sha256.New()
	if err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(data)
		return nil
	}); err != nil {
		panic(fmt.Sprintf("hash embedded engine: %v", err))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func Release(files fs.FS, baseDir, name, mainScript, applyScript string) (Runtime, error) {
	return ReleaseVersion(files, baseDir, name, Version(files), mainScript, applyScript)
}

func ReleaseVersion(files fs.FS, baseDir, name, version, mainScript, applyScript string) (Runtime, error) {
	root := filepath.Join(baseDir, name+"-"+version[:16])
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return runtimeAt(root, version, mainScript, applyScript), nil
	} else if err != nil && !os.IsNotExist(err) {
		return Runtime{}, err
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return Runtime{}, err
	}
	temporary, err := os.MkdirTemp(baseDir, "."+name+"-")
	if err != nil {
		return Runtime{}, err
	}
	defer os.RemoveAll(temporary)
	if err := extract(files, temporary); err != nil {
		return Runtime{}, err
	}
	if err := os.WriteFile(filepath.Join(temporary, "ENGINE_VERSION"), []byte(version+"\n"), 0o644); err != nil {
		return Runtime{}, err
	}
	if err := os.Rename(temporary, root); err != nil {
		if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
			return Runtime{}, err
		}
	}
	return runtimeAt(root, version, mainScript, applyScript), nil
}

func extract(files fs.FS, root string) error {
	return fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe embedded engine path %q", name)
		}
		target := filepath.Join(root, clean)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func runtimeAt(root, version, mainScript, applyScript string) Runtime {
	return Runtime{
		Version: version, Root: root,
		MainScript:  filepath.Join(root, filepath.FromSlash(mainScript)),
		ApplyScript: filepath.Join(root, filepath.FromSlash(applyScript)),
	}
}
