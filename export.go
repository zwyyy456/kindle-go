package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func BuildFlashcard(record KindleRecord, candidate FlashDictSenseCandidate) (FlashcardExportCard, error) {
	if strings.TrimSpace(candidate.DictionaryStableID) == "" {
		return FlashcardExportCard{}, errors.New("candidate missing dictionaryStableID")
	}
	if strings.TrimSpace(candidate.SenseHTML) == "" {
		return FlashcardExportCard{}, errors.New("candidate missing senseHtml")
	}
	senseIndex := 0
	if candidate.SenseIndex != nil {
		senseIndex = *candidate.SenseIndex
	}
	now := time.Now().UnixMilli()
	headerHTML := ""
	if candidate.HeaderHTML != nil {
		headerHTML = *candidate.HeaderHTML
	}
	return FlashcardExportCard{
		CardID:              NewUUIDString(),
		DictionaryStableID:  candidate.DictionaryStableID,
		Term:                candidate.Term,
		DictName:            candidate.DictName,
		Sentence:            record.Usage,
		SourceURL:           nil,
		UserNote:            nil,
		RenderMode:          "simple",
		HeaderHTML:          headerHTML,
		SubHeaderHTML:       candidate.SubHeaderHTML,
		PhraseHeaderHTML:    candidate.PhraseHeaderHTML,
		SenseHTML:           candidate.SenseHTML,
		EntryHTML:           "",
		ResourceTagsVersion: candidate.ResourceTagsVersion,
		CSSTagsHTML:         candidate.CSSTagsHTML,
		ScriptTagsHTML:      candidate.ScriptTagsHTML,
		SenseIndex:          senseIndex,
		SubsenseSelector:    candidate.SubsenseSelector,
		LearningState:       "new",
		CreatedAt:           now,
		UpdatedAt:           now,
	}, nil
}

func WriteFlashcardEnvelope(path string, cards []FlashcardExportCard) error {
	envelope := FlashcardExportEnvelope{
		SchemaVersion: 3,
		ExportedAt:    time.Now().UnixMilli(),
		Cards:         cards,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func WriteJSONL(path string, records []ReviewRecord) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	return nil
}

func PrintSummary(summary Summary, cfg Config) {
	fmt.Printf("raw_records=%d\n", summary.RawRecords)
	fmt.Printf("skipped_missing_usage=%d\n", summary.SkippedMissingUsage)
	fmt.Printf("deduped_records=%d\n", summary.DedupedRecords)
	fmt.Printf("lookup_failures=%d\n", summary.LookupFailures)
	fmt.Printf("ai_low_confidence=%d\n", summary.AILowConfidence)
	fmt.Printf("ai_invalid=%d\n", summary.AIInvalid)
	fmt.Printf("cards=%d\n", summary.Cards)
	fmt.Printf("review_records=%d\n", summary.ReviewRecords)
	fmt.Printf("output=%s\n", cfg.Output.Path)
	fmt.Printf("review=%s\n", cfg.Output.ReviewPath)
}

func NewUUIDString() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32])
}
