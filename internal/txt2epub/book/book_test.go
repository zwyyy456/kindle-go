package book

import (
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/config"
	txt "github.com/flashdict/kindle2flashdict/internal/txt2epub/text"
)

func TestBuildSplitsByLevel2WhenPresent(t *testing.T) {
	cfg := config.Defaults()
	parser, err := NewParser(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parser.Build([]string{
		"第一卷 风起",
		"第1章 开始",
		"正文。",
		"第2章 继续",
		"正文。",
	}, cfg, txt.Stats{})
	if err != nil {
		t.Fatal(err)
	}
	if b.SplitLevelUsed != 2 {
		t.Fatalf("SplitLevelUsed = %d", b.SplitLevelUsed)
	}
	if len(b.Sections) != 3 {
		t.Fatalf("sections = %d", len(b.Sections))
	}
	if b.Stats.H1Count != 1 || b.Stats.H2Count != 2 {
		t.Fatalf("stats = %#v", b.Stats)
	}
}

func TestBuildFallsBackToLevel1(t *testing.T) {
	cfg := config.Defaults()
	parser, err := NewParser(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parser.Build([]string{"第一卷 风起", "正文。", "第二卷 云涌", "正文。"}, cfg, txt.Stats{})
	if err != nil {
		t.Fatal(err)
	}
	if b.SplitLevelUsed != 1 {
		t.Fatalf("SplitLevelUsed = %d", b.SplitLevelUsed)
	}
	if len(b.Sections) != 2 {
		t.Fatalf("sections = %d", len(b.Sections))
	}
}
