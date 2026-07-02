package text

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zwyyy/txt2epub/internal/config"
)

type Stats struct {
	OriginalLines int
	DroppedLines  int
	BlankLines    int
	MergedLines   int
	Charset       string
}

type Cleaner struct {
	drop    []*regexp.Regexp
	replace []replaceRule
}

type replaceRule struct {
	pattern *regexp.Regexp
	with    string
}

func NewCleaner(cfg config.Config) (*Cleaner, error) {
	c := &Cleaner{}
	for _, pattern := range cfg.DropRegex {
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile drop_regex %q: %w", pattern, err)
		}
		c.drop = append(c.drop, re)
	}
	for _, rule := range cfg.Replace {
		if strings.TrimSpace(rule.Pattern) == "" {
			continue
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("compile replace pattern %q: %w", rule.Pattern, err)
		}
		c.replace = append(c.replace, replaceRule{pattern: re, with: rule.With})
	}
	return c, nil
}

func (c *Cleaner) Clean(input string, cfg config.Config, isHeading func(string) bool) ([]string, Stats) {
	rawLines := strings.Split(input, "\n")
	stats := Stats{OriginalLines: len(rawLines)}
	lines := make([]string, 0, len(rawLines))
	previousBlank := false

	for _, raw := range rawLines {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		for _, rule := range c.replace {
			line = rule.pattern.ReplaceAllString(line, rule.with)
		}
		if c.shouldDrop(line) {
			stats.DroppedLines++
			continue
		}
		if line == "" {
			stats.BlankLines++
			if cfg.TrimBlankLines {
				if previousBlank {
					continue
				}
				previousBlank = true
			}
			lines = append(lines, "")
			continue
		}
		previousBlank = false
		lines = append(lines, line)
	}

	if cfg.MergeLines {
		var merged int
		lines, merged = mergeHardWrappedLines(lines, isHeading)
		stats.MergedLines = merged
	}
	return lines, stats
}

func (c *Cleaner) shouldDrop(line string) bool {
	for _, re := range c.drop {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func mergeHardWrappedLines(lines []string, isHeading func(string) bool) ([]string, int) {
	var out []string
	var current string
	merged := 0

	flush := func() {
		if current != "" {
			out = append(out, current)
			current = ""
		}
	}

	for _, line := range lines {
		if line == "" {
			flush()
			out = append(out, "")
			continue
		}
		if isHeading(line) {
			flush()
			out = append(out, line)
			continue
		}
		if current == "" {
			current = line
			if endsParagraph(line) {
				flush()
			}
			continue
		}
		current += line
		merged++
		if endsParagraph(line) {
			flush()
		}
	}
	flush()
	return out, merged
}

func endsParagraph(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	runes := []rune(line)
	last := runes[len(runes)-1]
	return strings.ContainsRune("。！？；：.!?;:」』”’）】》…", last)
}
