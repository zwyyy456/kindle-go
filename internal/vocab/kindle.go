package vocab

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func ReadKindleRecords(path string) ([]KindleRecord, int, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()

	query := `
SELECT
  COALESCE(NULLIF(w.stem, ''), NULLIF(w.word, ''), l.word_key) AS term,
  COALESCE(l.usage, '') AS usage,
  COALESCE(b.title, '') AS book_title,
  COALESCE(CAST(l.pos AS TEXT), '') AS location,
  COALESCE(CAST(l.timestamp AS INTEGER), 0) AS timestamp
FROM LOOKUPS l
LEFT JOIN WORDS w ON w.id = l.word_key
LEFT JOIN BOOK_INFO b ON b.id = l.book_key
ORDER BY timestamp DESC`
	rows, err := db.Query(query)
	if err != nil {
		return nil, 0, fmt.Errorf("read Kindle vocab.db: %w", err)
	}
	defer rows.Close()

	var records []KindleRecord
	skippedMissingUsage := 0
	seq := 0
	for rows.Next() {
		var record KindleRecord
		if err := rows.Scan(&record.Term, &record.Usage, &record.BookTitle, &record.Location, &record.Timestamp); err != nil {
			return nil, 0, err
		}
		record.Term = strings.TrimSpace(record.Term)
		record.Usage = strings.TrimSpace(record.Usage)
		if record.Term == "" {
			continue
		}
		if record.Usage == "" {
			skippedMissingUsage++
			continue
		}
		seq++
		record.RequestID = fmt.Sprintf("kindle-%d", seq)
		records = append(records, record)
	}
	return records, skippedMissingUsage, rows.Err()
}

func DeduplicateRecords(records []KindleRecord) []KindleRecord {
	seen := map[string]KindleRecord{}
	order := []string{}
	for _, record := range records {
		key := normalizeKey(record.Term) + "\x00" + normalizeKey(record.Usage)
		existing, ok := seen[key]
		if !ok {
			seen[key] = record
			order = append(order, key)
			continue
		}
		if preferRecord(record, existing) {
			seen[key] = record
		}
	}
	deduped := make([]KindleRecord, 0, len(order))
	for _, key := range order {
		deduped = append(deduped, seen[key])
	}
	return deduped
}

func normalizeKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func preferRecord(candidate, existing KindleRecord) bool {
	if candidate.Timestamp != existing.Timestamp {
		return candidate.Timestamp > existing.Timestamp
	}
	return len(candidate.BookTitle)+len(candidate.Location) > len(existing.BookTitle)+len(existing.Location)
}
