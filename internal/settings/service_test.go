package settings

import (
	"context"
	"strings"
	"testing"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/store"
)

func TestInitializeSeedsConfigAndSavedValuesOverrideFutureConfig(t *testing.T) {
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	base := txtconfig.Defaults()
	base.Metadata.Author = "配置作者"
	base.Style.LineHeight = 1.9
	service := New(storage, base, Runtime{LibraryDir: "/library", LibrarySource: "config file"})
	if err := service.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	values, err := service.Current(context.Background())
	if err != nil || values.Author != "配置作者" || values.Style.LineHeight != 1.9 || values.Proofread.BatchSize != 12000 || values.Proofread.Concurrency != 3 {
		t.Fatalf("seeded values = %#v, %v", values, err)
	}
	values.Author = "数据库作者"
	values.Style.LineHeight = 2.2
	values.KindleShowEPUB = true
	if err := service.Save(context.Background(), values); err != nil {
		t.Fatal(err)
	}

	changedBase := txtconfig.Defaults()
	changedBase.Metadata.Author = "另一个配置作者"
	loaded, err := New(storage, changedBase, Runtime{}).Current(context.Background())
	if err != nil || loaded.Author != "数据库作者" || loaded.Style.LineHeight != 2.2 || !loaded.KindleShowEPUB {
		t.Fatalf("persisted values = %#v, %v", loaded, err)
	}
}

func TestSaveRejectsInvalidRangesAndRegex(t *testing.T) {
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	service := New(storage, txtconfig.Defaults(), Runtime{})
	values := service.seed
	values.TXT.H1Regex = "["
	if err := service.Save(context.Background(), values); err == nil || !strings.Contains(err.Error(), "H1 regex") {
		t.Fatalf("invalid regex error = %v", err)
	}
	values = service.seed
	values.Proofread.Concurrency = 9
	if err := service.Save(context.Background(), values); err == nil || !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("invalid concurrency error = %v", err)
	}
}
