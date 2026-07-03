package vocab

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func LoadConfig(path string) (Config, error) {
	cfg := Config{}
	cfg.SenseSource.Type = "flashdict-lookup-bridge"
	cfg.SenseSource.DiscoveryPath = DefaultFlashDictLookupBridgeDiscoveryPath()
	cfg.SenseSource.DictionaryPolicy = "first-extractable-enabled"
	cfg.AI.Backend = "codex-exec"
	cfg.AI.Model = "gpt-5.4-mini"
	cfg.AI.CodexPath = "codex"
	cfg.AI.BatchSize = 30
	cfg.AI.MinConfidence = 0.75
	cfg.Output.Type = "flashdict-json"
	cfg.Output.Path = "flashdict-cards.json"
	cfg.Output.ReviewPath = "review.jsonl"

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	values, err := parseSimpleTOML(string(data))
	if err != nil {
		return cfg, err
	}
	for key, value := range values {
		switch key {
		case "kindle.vocab_db":
			cfg.Kindle.VocabDB = value
		case "sense_source.type":
			cfg.SenseSource.Type = value
		case "sense_source.discovery_path":
			if value != "" {
				cfg.SenseSource.DiscoveryPath = value
			}
		case "sense_source.socket_path":
			cfg.SenseSource.SocketPath = value
		case "sense_source.dictionary_policy":
			cfg.SenseSource.DictionaryPolicy = value
		case "ai.backend":
			cfg.AI.Backend = value
		case "ai.model":
			cfg.AI.Model = value
		case "ai.codex_path":
			cfg.AI.CodexPath = value
		case "ai.batch_size":
			cfg.AI.BatchSize, _ = strconv.Atoi(value)
		case "ai.min_confidence":
			cfg.AI.MinConfidence, _ = strconv.ParseFloat(value, 64)
		case "output.type":
			cfg.Output.Type = value
		case "output.path":
			cfg.Output.Path = value
		case "output.review_path":
			cfg.Output.ReviewPath = value
		}
	}
	return cfg, nil
}

func DefaultFlashDictLookupBridgeDiscoveryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(
		home,
		"Library",
		"Containers",
		"tech.hyperseek.flashdict",
		"Data",
		"Library",
		"Application Support",
		"FlashDict",
		"lookup-bridge.json",
	)
}

func parseSimpleTOML(text string) (map[string]string, error) {
	values := map[string]string{}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid config line: %s", line)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, err
			}
			value = unquoted
		}
		if section != "" {
			key = section + "." + key
		}
		values[key] = value
	}
	return values, scanner.Err()
}

func stripComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func validateConfig(cfg Config) error {
	if cfg.Kindle.VocabDB == "" {
		return errors.New("kindle.vocab_db is required")
	}
	if cfg.SenseSource.Type != "flashdict-lookup-bridge" {
		return fmt.Errorf("unsupported sense_source.type: %s", cfg.SenseSource.Type)
	}
	if cfg.SenseSource.DiscoveryPath == "" && cfg.SenseSource.SocketPath == "" {
		return errors.New("sense_source.discovery_path is required unless sense_source.socket_path is set")
	}
	if cfg.SenseSource.DictionaryPolicy != "first-extractable-enabled" {
		return fmt.Errorf("unsupported sense_source.dictionary_policy: %s", cfg.SenseSource.DictionaryPolicy)
	}
	if cfg.AI.Backend != "codex-exec" {
		return fmt.Errorf("unsupported ai.backend: %s", cfg.AI.Backend)
	}
	if cfg.Output.Type != "flashdict-json" {
		return fmt.Errorf("unsupported output.type: %s", cfg.Output.Type)
	}
	if cfg.AI.BatchSize <= 0 {
		return errors.New("ai.batch_size must be positive")
	}
	if cfg.AI.MinConfidence <= 0 || cfg.AI.MinConfidence > 1 {
		return errors.New("ai.min_confidence must be in (0, 1]")
	}
	return nil
}
