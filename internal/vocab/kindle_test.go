package vocab

import "testing"

func TestDeduplicateRecordsKeepsOrderAndPrefersNewest(t *testing.T) {
	records := []KindleRecord{
		{RequestID: "old", Term: " Example ", Usage: "A sentence.", BookTitle: "Book A", Timestamp: 10},
		{RequestID: "other", Term: "different", Usage: "A sentence.", BookTitle: "Book B", Timestamp: 11},
		{RequestID: "new", Term: "example", Usage: " A   sentence. ", BookTitle: "Book C", Timestamp: 20},
	}

	deduped := DeduplicateRecords(records)

	if len(deduped) != 2 {
		t.Fatalf("len(deduped) = %d", len(deduped))
	}
	if deduped[0].RequestID != "new" {
		t.Fatalf("first duplicate winner = %q", deduped[0].RequestID)
	}
	if deduped[1].RequestID != "other" {
		t.Fatalf("second record = %q", deduped[1].RequestID)
	}
}

func TestDeduplicateRecordsTieBreaksOnMetadataLength(t *testing.T) {
	records := []KindleRecord{
		{RequestID: "short", Term: "term", Usage: "usage", BookTitle: "A", Location: "1", Timestamp: 10},
		{RequestID: "long", Term: "term", Usage: "usage", BookTitle: "Longer Book", Location: "100", Timestamp: 10},
	}

	deduped := DeduplicateRecords(records)

	if len(deduped) != 1 {
		t.Fatalf("len(deduped) = %d", len(deduped))
	}
	if deduped[0].RequestID != "long" {
		t.Fatalf("duplicate winner = %q", deduped[0].RequestID)
	}
}
