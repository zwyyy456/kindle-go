package calibre

import (
	"fmt"
	"os/exec"

	"github.com/zwyyy/txt2epub/internal/config"
)

func Convert(epubPath, outputPath string, cfg config.Config) error {
	args := []string{epubPath, outputPath}
	if cfg.Calibre.OutputProfile != "" {
		args = append(args, "--output-profile", cfg.Calibre.OutputProfile)
	}
	if cfg.Language != "" {
		args = append(args, "--language", cfg.Language)
	}
	args = append(args, "--chapter-mark", "pagebreak")
	args = append(args, cfg.Calibre.ExtraArgs...)

	cmd := exec.Command(cfg.Calibre.Path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s: %w\n%s", cfg.Calibre.Path, err, string(out))
	}
	return nil
}
