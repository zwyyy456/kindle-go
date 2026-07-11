package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const (
	DefaultH1Regex = `^\s*(第[一二三四五六七八九十百千万零〇两\d]+[卷部集篇]).*$`
	DefaultH2Regex = `^\s*(第[一二三四五六七八九十百千万零〇两\d]+[章节回]).*$`
)

type Config struct {
	Title          string        `toml:"title" yaml:"title"`
	Author         string        `toml:"author" yaml:"author"`
	Language       string        `toml:"language" yaml:"language"`
	H1Regex        string        `toml:"h1_regex" yaml:"h1_regex"`
	H2Regex        string        `toml:"h2_regex" yaml:"h2_regex"`
	Output         string        `toml:"output" yaml:"output"`
	Format         string        `toml:"format" yaml:"format"`
	MergeLines     bool          `toml:"merge_lines" yaml:"merge_lines"`
	TrimBlankLines bool          `toml:"trim_blank_lines" yaml:"trim_blank_lines"`
	SplitLevel     int           `toml:"split_level" yaml:"split_level"`
	Cover          bool          `toml:"cover" yaml:"cover"`
	DropRegex      []string      `toml:"drop_regex" yaml:"drop_regex"`
	Replace        []ReplaceRule `toml:"replace" yaml:"replace"`
	Style          Style         `toml:"style" yaml:"style"`
}

type ReplaceRule struct {
	Pattern string `toml:"pattern" yaml:"pattern"`
	With    string `toml:"with" yaml:"with"`
}

type Style struct {
	LineHeight       float64 `toml:"line_height" yaml:"line_height"`
	ParagraphIndent  string  `toml:"paragraph_indent" yaml:"paragraph_indent"`
	ParagraphSpacing string  `toml:"paragraph_spacing" yaml:"paragraph_spacing"`
	TextAlign        string  `toml:"text_align" yaml:"text_align"`
}

func Defaults() Config {
	return Config{
		Language:       "zh-CN",
		H1Regex:        DefaultH1Regex,
		H2Regex:        DefaultH2Regex,
		Format:         "epub",
		MergeLines:     true,
		TrimBlankLines: true,
		SplitLevel:     2,
		Cover:          true,
		Style: Style{
			LineHeight:       1.7,
			ParagraphIndent:  "2em",
			ParagraphSpacing: "0",
			TextAlign:        "justify",
		},
	}
}

func Load(path string) (Config, string, error) {
	cfg := Defaults()
	resolved, err := ResolveConfigPath(path)
	if err != nil {
		return cfg, "", err
	}
	if resolved == "" {
		return cfg, "", nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return cfg, resolved, err
	}
	if err := decodeConfig(resolved, data, &cfg); err != nil {
		return cfg, resolved, err
	}
	Normalize(&cfg)
	return cfg, resolved, nil
}

func ResolveConfigPath(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return path, nil
	}
	for _, name := range []string{"txt2epub.toml", "txt2epub.yaml", "txt2epub.yml"} {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

func Normalize(cfg *Config) {
	cfg.Format = strings.ToLower(strings.TrimSpace(cfg.Format))
	if cfg.Format == "" {
		cfg.Format = "epub"
	}
	cfg.Language = strings.TrimSpace(cfg.Language)
	if cfg.Language == "" {
		cfg.Language = "zh-CN"
	}
	if strings.TrimSpace(cfg.H1Regex) == "" {
		cfg.H1Regex = DefaultH1Regex
	}
	if strings.TrimSpace(cfg.H2Regex) == "" {
		cfg.H2Regex = DefaultH2Regex
	}
	if cfg.SplitLevel < 1 || cfg.SplitLevel > 2 {
		cfg.SplitLevel = 2
	}
	if cfg.Style.LineHeight <= 0 {
		cfg.Style.LineHeight = 1.7
	}
	if cfg.Style.ParagraphIndent == "" {
		cfg.Style.ParagraphIndent = "2em"
	}
	if cfg.Style.ParagraphSpacing == "" {
		cfg.Style.ParagraphSpacing = "0"
	}
	if cfg.Style.TextAlign == "" {
		cfg.Style.TextAlign = "justify"
	}
}

func OutputPath(input string, cfg Config) string {
	if cfg.Output != "" {
		return cfg.Output
	}
	ext := ".epub"
	if cfg.Format == "azw3" {
		ext = ".azw3"
	}
	base := strings.TrimSuffix(input, filepath.Ext(input))
	return base + ext
}

func decodeConfig(path string, data []byte, cfg *Config) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		return toml.Unmarshal(data, cfg)
	case ".yaml", ".yml":
		return yaml.Unmarshal(data, cfg)
	default:
		return fmt.Errorf("unsupported config format %q", filepath.Ext(path))
	}
}
