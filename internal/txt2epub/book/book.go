package book

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/config"
	txt "github.com/flashdict/kindle2flashdict/internal/txt2epub/text"
)

type Book struct {
	Title          string
	Author         string
	Language       string
	Cover          bool
	Style          config.Style
	Sections       []Section
	Headings       []Heading
	SplitLevelUsed int
	Stats          Stats
}

type Section struct {
	ID     string
	Title  string
	Level  int
	Blocks []Block
}

type Block struct {
	Kind  BlockKind
	Level int
	Text  string
	ID    string
}

type BlockKind string

const (
	BlockHeading   BlockKind = "heading"
	BlockParagraph BlockKind = "paragraph"
)

type Heading struct {
	ID        string
	Title     string
	Level     int
	SectionID string
}

type Stats struct {
	Text           txt.Stats
	H1Count        int
	H2Count        int
	SectionCount   int
	ParagraphCount int
}

type Parser struct {
	h1 *regexp.Regexp
	h2 *regexp.Regexp
}

func NewParser(cfg config.Config) (*Parser, error) {
	h1, err := regexp.Compile(cfg.TXT.H1Regex)
	if err != nil {
		return nil, fmt.Errorf("compile h1_regex: %w", err)
	}
	h2, err := regexp.Compile(cfg.TXT.H2Regex)
	if err != nil {
		return nil, fmt.Errorf("compile h2_regex: %w", err)
	}
	return &Parser{h1: h1, h2: h2}, nil
}

func (p *Parser) IsHeading(line string) bool {
	return p.h1.MatchString(line) || p.h2.MatchString(line)
}

func (p *Parser) Build(lines []string, cfg config.Config, textStats txt.Stats) (Book, error) {
	blocks := make([]Block, 0, len(lines))
	stats := Stats{Text: textStats}

	for _, line := range lines {
		if line == "" {
			continue
		}
		if p.h1.MatchString(line) {
			stats.H1Count++
			blocks = append(blocks, Block{Kind: BlockHeading, Level: 1, Text: line})
			continue
		}
		if p.h2.MatchString(line) {
			stats.H2Count++
			blocks = append(blocks, Block{Kind: BlockHeading, Level: 2, Text: line})
			continue
		}
		stats.ParagraphCount++
		blocks = append(blocks, Block{Kind: BlockParagraph, Text: line})
	}

	splitLevel := 1
	if cfg.TXT.SplitLevel >= 2 && stats.H2Count > 0 {
		splitLevel = 2
	}
	sections, headings := splitBlocks(blocks, splitLevel)
	if len(sections) == 0 {
		sections = []Section{{
			ID:    "chapter-001",
			Title: "正文",
			Level: 1,
		}}
	}

	stats.SectionCount = len(sections)
	book := Book{
		Title:          cfg.Metadata.Title,
		Author:         cfg.Metadata.Author,
		Language:       cfg.Metadata.Language,
		Cover:          cfg.Output.Cover,
		Style:          cfg.Style,
		Sections:       sections,
		Headings:       headings,
		SplitLevelUsed: splitLevel,
		Stats:          stats,
	}
	return book, nil
}

func splitBlocks(blocks []Block, splitLevel int) ([]Section, []Heading) {
	var sections []Section
	var headings []Heading
	var current *Section
	sectionSeq := 0
	headingSeq := 0

	startSection := func(title string, level int) *Section {
		sectionSeq++
		sec := Section{
			ID:    fmt.Sprintf("chapter-%03d", sectionSeq),
			Title: title,
			Level: level,
		}
		sections = append(sections, sec)
		return &sections[len(sections)-1]
	}

	for _, block := range blocks {
		if block.Kind == BlockHeading && block.Level <= splitLevel {
			current = startSection(block.Text, block.Level)
		}
		if current == nil {
			current = startSection("正文", 1)
		}
		if block.Kind == BlockHeading {
			headingSeq++
			block.ID = fmt.Sprintf("heading-%03d", headingSeq)
			headings = append(headings, Heading{
				ID:        block.ID,
				Title:     block.Text,
				Level:     block.Level,
				SectionID: current.ID,
			})
			if current.Title == "正文" {
				current.Title = block.Text
				current.Level = block.Level
			}
		}
		current.Blocks = append(current.Blocks, block)
	}
	return sections, headings
}

func (b Book) EffectiveTitle(inputPath string) string {
	if strings.TrimSpace(b.Title) != "" {
		return b.Title
	}
	return strings.TrimSuffix(inputPath, ".txt")
}
