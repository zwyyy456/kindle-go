package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/store"
)

const globalKey = "web.global_defaults"

type Proofread struct {
	Model       string `json:"model"`
	BatchSize   int    `json:"batch_size"`
	Concurrency int    `json:"concurrency"`
}

type Values struct {
	Author         string              `json:"author"`
	Language       string              `json:"language"`
	Cover          bool                `json:"cover"`
	TXT            txtconfig.TXTConfig `json:"txt"`
	Style          txtconfig.Style     `json:"style"`
	KindleShowEPUB bool                `json:"kindle_show_epub"`
	Proofread      Proofread           `json:"proofread"`
	UpdatedAt      time.Time           `json:"-"`
}

type Runtime struct {
	LibraryDir, LibrarySource string
	WebAddr, WebAddrSource    string
	KindleAddr, KindleSource  string
	ConfigPath                string
}

type Service struct {
	store   *store.Store
	seed    Values
	runtime Runtime
	now     func() time.Time
}

func New(source *store.Store, base txtconfig.Config, runtime Runtime) *Service {
	txtconfig.Normalize(&base)
	return &Service{
		store: source,
		seed: Values{
			Author: base.Metadata.Author, Language: base.Metadata.Language, Cover: base.Output.Cover, TXT: base.TXT, Style: base.Style,
			Proofread: Proofread{BatchSize: 12000, Concurrency: 3},
		},
		runtime: runtime,
		now:     time.Now,
	}
}

func (s *Service) Initialize(ctx context.Context) error {
	values := s.seed
	normalize(&values)
	if err := Validate(values); err != nil {
		return err
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return s.store.SeedSetting(ctx, globalKey, string(encoded), s.now())
}

func (s *Service) Current(ctx context.Context) (Values, error) {
	raw, updatedAt, found, err := s.store.Setting(ctx, globalKey)
	if err != nil {
		return Values{}, err
	}
	if !found {
		values := s.seed
		values.UpdatedAt = updatedAt
		return values, nil
	}
	var values Values
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return Values{}, fmt.Errorf("decode global settings: %w", err)
	}
	values.UpdatedAt = updatedAt
	return values, nil
}

func (s *Service) Save(ctx context.Context, values Values) error {
	normalize(&values)
	if err := Validate(values); err != nil {
		return err
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return s.store.SaveSetting(ctx, globalKey, string(encoded), s.now())
}

func (s *Service) ConversionConfig(ctx context.Context) (txtconfig.Config, error) {
	values, err := s.Current(ctx)
	if err != nil {
		return txtconfig.Config{}, err
	}
	cfg := txtconfig.Defaults()
	cfg.Metadata.Author = values.Author
	cfg.Metadata.Language = values.Language
	cfg.Output.Cover = values.Cover
	cfg.TXT = values.TXT
	cfg.Style = values.Style
	txtconfig.Normalize(&cfg)
	return cfg, nil
}

func (s *Service) Runtime() Runtime { return s.runtime }

func normalize(values *Values) {
	values.Author = strings.TrimSpace(values.Author)
	values.Language = strings.TrimSpace(values.Language)
	values.Proofread.Model = strings.TrimSpace(values.Proofread.Model)
	values.TXT.H1Regex = strings.TrimSpace(values.TXT.H1Regex)
	values.TXT.H2Regex = strings.TrimSpace(values.TXT.H2Regex)
	values.Style.ParagraphIndent = strings.TrimSpace(values.Style.ParagraphIndent)
	values.Style.ParagraphSpacing = strings.TrimSpace(values.Style.ParagraphSpacing)
	values.Style.TextAlign = strings.ToLower(strings.TrimSpace(values.Style.TextAlign))
}

func Validate(values Values) error {
	if values.Language == "" {
		return fmt.Errorf("language is required")
	}
	for name, pattern := range map[string]string{"H1 regex": values.TXT.H1Regex, "H2 regex": values.TXT.H2Regex} {
		if pattern == "" {
			return fmt.Errorf("%s is required", name)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%s is invalid: %w", name, err)
		}
	}
	for index, pattern := range values.TXT.DropRegex {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("drop regex %d is invalid: %w", index+1, err)
		}
	}
	for index, rule := range values.TXT.Replace {
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return fmt.Errorf("replacement regex %d is invalid: %w", index+1, err)
		}
	}
	if values.TXT.SplitLevel < 1 || values.TXT.SplitLevel > 2 {
		return fmt.Errorf("split level must be 1 or 2")
	}
	if values.Style.LineHeight < 0.5 || values.Style.LineHeight > 4 {
		return fmt.Errorf("line height must be between 0.5 and 4")
	}
	if values.Style.ParagraphIndent == "" || values.Style.ParagraphSpacing == "" {
		return fmt.Errorf("paragraph indent and spacing are required")
	}
	switch values.Style.TextAlign {
	case "left", "right", "center", "justify":
	default:
		return fmt.Errorf("text align must be left, right, center, or justify")
	}
	if values.Proofread.BatchSize < 1000 || values.Proofread.BatchSize > 50000 {
		return fmt.Errorf("proofread batch size must be between 1000 and 50000 characters")
	}
	if values.Proofread.Concurrency < 1 || values.Proofread.Concurrency > 8 {
		return fmt.Errorf("proofread concurrency must be between 1 and 8")
	}
	return nil
}
