package vocab

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildFlashcardMapsRecordAndCandidate(t *testing.T) {
	header := "<h1>example</h1>"
	subHeader := "<p>noun</p>"
	senseIndex := 3
	card, err := BuildFlashcard(
		KindleRecord{Usage: "This is an example."},
		FlashDictSenseCandidate{
			DictionaryStableID:  "dict-1",
			DictName:            "Test Dictionary",
			Term:                "example",
			HeaderHTML:          &header,
			SubHeaderHTML:       &subHeader,
			SenseHTML:           "<div>a thing characteristic of its kind</div>",
			SenseIndex:          &senseIndex,
			ResourceTagsVersion: 2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if card.CardID == "" {
		t.Fatal("CardID is empty")
	}
	if card.DictionaryStableID != "dict-1" || card.Term != "example" || card.DictName != "Test Dictionary" {
		t.Fatalf("unexpected card identity: %+v", card)
	}
	if card.Sentence != "This is an example." {
		t.Fatalf("Sentence = %q", card.Sentence)
	}
	if card.HeaderHTML != header || card.SubHeaderHTML != &subHeader {
		t.Fatalf("unexpected header fields: %+v", card)
	}
	if card.SenseIndex != senseIndex {
		t.Fatalf("SenseIndex = %d", card.SenseIndex)
	}
	if card.LearningState != "new" || card.RenderMode != "simple" {
		t.Fatalf("unexpected defaults: %+v", card)
	}
}

func TestBuildFlashcardRequiresDictionaryAndSense(t *testing.T) {
	if _, err := BuildFlashcard(KindleRecord{}, FlashDictSenseCandidate{SenseHTML: "<div>x</div>"}); err == nil {
		t.Fatal("missing dictionaryStableID returned nil error")
	}
	if _, err := BuildFlashcard(KindleRecord{}, FlashDictSenseCandidate{DictionaryStableID: "dict-1"}); err == nil {
		t.Fatal("missing senseHtml returned nil error")
	}
}

func TestPrintSummaryWritesAllCounters(t *testing.T) {
	var out bytes.Buffer
	PrintSummary(&out, Summary{
		RawRecords:          10,
		SkippedMissingUsage: 2,
		DedupedRecords:      7,
		LookupFailures:      1,
		AILowConfidence:     3,
		AIInvalid:           4,
		Cards:               5,
		ReviewRecords:       6,
	}, Config{Output: OutputConfig{Path: "cards.json", ReviewPath: "review.jsonl"}})

	for _, want := range []string{
		"raw_records=10",
		"skipped_missing_usage=2",
		"deduped_records=7",
		"lookup_failures=1",
		"ai_low_confidence=3",
		"ai_invalid=4",
		"cards=5",
		"review_records=6",
		"output=cards.json",
		"review=review.jsonl",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q: %s", want, out.String())
		}
	}
}
