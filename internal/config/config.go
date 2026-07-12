package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const (
	CurrentVersion = 1
	DefaultH1Regex = `^\s*(第[一二三四五六七八九十百千万零〇两\d]+[卷部集篇]).*$`
	DefaultH2Regex = `^\s*(第[一二三四五六七八九十百千万零〇两\d]+[章节回]).*$`
)

type Config struct {
	Version  int            `toml:"version" yaml:"version"`
	Metadata MetadataConfig `toml:"metadata" yaml:"metadata"`
	Output   OutputConfig   `toml:"output" yaml:"output"`
	TXT      TXTConfig      `toml:"txt" yaml:"txt"`
	Style    Style          `toml:"style" yaml:"style"`
	Server   ServerConfig   `toml:"server" yaml:"server"`
}

type MetadataConfig struct {
	Title    string `toml:"title" yaml:"title"`
	Author   string `toml:"author" yaml:"author"`
	Language string `toml:"language" yaml:"language"`
}

type OutputConfig struct {
	Path   string `toml:"path" yaml:"path"`
	Format string `toml:"format" yaml:"format"`
	Cover  bool   `toml:"cover" yaml:"cover"`
}

type TXTConfig struct {
	H1Regex        string        `toml:"h1_regex" yaml:"h1_regex"`
	H2Regex        string        `toml:"h2_regex" yaml:"h2_regex"`
	MergeLines     bool          `toml:"merge_lines" yaml:"merge_lines"`
	TrimBlankLines bool          `toml:"trim_blank_lines" yaml:"trim_blank_lines"`
	SplitLevel     int           `toml:"split_level" yaml:"split_level"`
	DropRegex      []string      `toml:"drop_regex" yaml:"drop_regex"`
	Replace        []ReplaceRule `toml:"replace" yaml:"replace"`
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

type ServerConfig struct {
	WebAddr    string `toml:"web_addr" yaml:"web_addr"`
	KindleAddr string `toml:"kindle_addr" yaml:"kindle_addr"`
	LibraryDir string `toml:"library_dir" yaml:"library_dir"`
}

func Defaults() Config {
	return Config{
		Version:  CurrentVersion,
		Metadata: MetadataConfig{Language: "zh-CN"},
		Output:   OutputConfig{Format: "epub", Cover: true},
		TXT: TXTConfig{
			H1Regex: DefaultH1Regex, H2Regex: DefaultH2Regex,
			MergeLines: true, TrimBlankLines: true, SplitLevel: 2,
		},
		Style: Style{
			LineHeight: 1.7, ParagraphIndent: "2em",
			ParagraphSpacing: "0", TextAlign: "justify",
		},
		Server: ServerConfig{
			WebAddr: ":8787", KindleAddr: ":8788", LibraryDir: "kindle-go-library",
		},
	}
}

func Load(path string) (Config, string, error) {
	cfg := Defaults()
	resolved, err := ResolveConfigPath(path)
	if err != nil || resolved == "" {
		return cfg, resolved, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return cfg, resolved, err
	}
	version, err := readVersion(resolved, data)
	if err != nil {
		return cfg, resolved, err
	}
	if version == nil {
		return cfg, resolved, fmt.Errorf("config version is required")
	}
	if *version != CurrentVersion {
		return cfg, resolved, fmt.Errorf("unsupported config version %d; expected %d", *version, CurrentVersion)
	}
	if err := decodeConfig(resolved, data, &cfg); err != nil {
		return cfg, resolved, err
	}
	Normalize(&cfg)
	if cfg.Output.Format != "epub" && cfg.Output.Format != "azw3" {
		return cfg, resolved, fmt.Errorf("unsupported output format %q", cfg.Output.Format)
	}
	return cfg, resolved, nil
}

func ResolveConfigPath(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return path, nil
	}
	for _, name := range []string{"kindle-go.toml", "kindle-go.yaml", "kindle-go.yml"} {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

func Normalize(cfg *Config) {
	cfg.Output.Format = strings.ToLower(strings.TrimSpace(cfg.Output.Format))
	if cfg.Output.Format == "" {
		cfg.Output.Format = "epub"
	}
	cfg.Metadata.Language = strings.TrimSpace(cfg.Metadata.Language)
	if cfg.Metadata.Language == "" {
		cfg.Metadata.Language = "zh-CN"
	}
	if strings.TrimSpace(cfg.TXT.H1Regex) == "" {
		cfg.TXT.H1Regex = DefaultH1Regex
	}
	if strings.TrimSpace(cfg.TXT.H2Regex) == "" {
		cfg.TXT.H2Regex = DefaultH2Regex
	}
	if cfg.TXT.SplitLevel < 1 || cfg.TXT.SplitLevel > 2 {
		cfg.TXT.SplitLevel = 2
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
	if cfg.Server.WebAddr == "" {
		cfg.Server.WebAddr = ":8787"
	}
	if cfg.Server.KindleAddr == "" {
		cfg.Server.KindleAddr = ":8788"
	}
	if cfg.Server.LibraryDir == "" {
		cfg.Server.LibraryDir = "kindle-go-library"
	}
}

func OutputPath(input string, cfg Config) string {
	if cfg.Output.Path != "" {
		return cfg.Output.Path
	}
	ext := ".epub"
	if cfg.Output.Format == "azw3" {
		ext = ".azw3"
	}
	return strings.TrimSuffix(input, filepath.Ext(input)) + ext
}

func decodeConfig(path string, data []byte, cfg *Config) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		decoder := toml.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		return decoder.Decode(cfg)
	case ".yaml", ".yml":
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		return decoder.Decode(cfg)
	default:
		return fmt.Errorf("unsupported config format %q", filepath.Ext(path))
	}
}

func readVersion(path string, data []byte) (*int, error) {
	var header struct {
		Version *int `toml:"version" yaml:"version"`
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		if err := toml.Unmarshal(data, &header); err != nil {
			return nil, err
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &header); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported config format %q", filepath.Ext(path))
	}
	return header.Version, nil
}
