package text

import (
	"reflect"
	"testing"

	"github.com/flashdict/kindle2flashdict/internal/txt2epub/config"
)

func TestCleanDropReplaceAndMerge(t *testing.T) {
	cfg := config.Defaults()
	cfg.DropRegex = []string{`^广告`}
	cfg.Replace = []config.ReplaceRule{{Pattern: `　+`, With: " "}}

	cleaner, err := NewCleaner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	lines, stats := cleaner.Clean("第一章 开始\n广告请收藏\n这是一个很长的段落\n被硬换行切开。\n\n\n下一段。", cfg, func(s string) bool {
		return s == "第一章 开始"
	})

	want := []string{"第一章 开始", "这是一个很长的段落被硬换行切开。", "", "下一段。"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %#v", lines)
	}
	if stats.DroppedLines != 1 {
		t.Fatalf("DroppedLines = %d", stats.DroppedLines)
	}
	if stats.MergedLines != 1 {
		t.Fatalf("MergedLines = %d", stats.MergedLines)
	}
}
