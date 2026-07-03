package vocab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func SelectAndBuildCards(cfg Config, records []KindleRecord, lookups map[string]FlashDictLookupResponse) ([]FlashcardExportCard, []ReviewRecord, int, int, error) {
	var cards []FlashcardExportCard
	var reviews []ReviewRecord
	lowConfidence := 0
	invalid := 0
	recordByID := map[string]KindleRecord{}
	for _, record := range records {
		recordByID[record.RequestID] = record
	}

	items := make([]AIItem, 0, len(lookups))
	for _, record := range records {
		lookup, ok := lookups[record.RequestID]
		if !ok {
			continue
		}
		item := AIItem{
			RequestID: record.RequestID,
			Term:      record.Term,
			Usage:     record.Usage,
		}
		for _, candidate := range lookup.Candidates {
			item.Candidates = append(item.Candidates, AICandidate{
				CandidateID: candidate.CandidateID,
				SenseHTML:   candidate.SenseHTML,
			})
		}
		items = append(items, item)
	}

	for _, batch := range chunkAIItems(items, cfg.AI.BatchSize) {
		selectionBatch, err := RunCodexExec(cfg, batch)
		if err != nil {
			return nil, nil, 0, 0, err
		}
		for _, selection := range selectionBatch.Results {
			record := recordByID[selection.RequestID]
			lookup := lookups[selection.RequestID]
			candidate, ok := findCandidate(lookup.Candidates, selection.SelectedCandidateID)
			if !ok {
				invalid++
				reviews = append(reviews, reviewFromSelection(record, lookup.Candidates, selection, "ai_invalid_candidate", "selected candidate not found"))
				continue
			}
			if selection.Confidence < cfg.AI.MinConfidence || math.IsNaN(selection.Confidence) {
				lowConfidence++
				reviews = append(reviews, reviewFromSelection(record, lookup.Candidates, selection, "ai_low_confidence", "confidence below threshold"))
				continue
			}
			card, err := BuildFlashcard(record, candidate)
			if err != nil {
				invalid++
				reviews = append(reviews, reviewFromSelection(record, lookup.Candidates, selection, "card_build_failed", err.Error()))
				continue
			}
			cards = append(cards, card)
		}
	}
	return cards, reviews, lowConfidence, invalid, nil
}

func chunkAIItems(items []AIItem, size int) [][]AIItem {
	var chunks [][]AIItem
	for start := 0; start < len(items); start += size {
		end := start + size
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[start:end])
	}
	return chunks
}

func RunCodexExec(cfg Config, items []AIItem) (AISelectionBatch, error) {
	tmpDir, err := os.MkdirTemp("", "kindle2flashdict-codex-")
	if err != nil {
		return AISelectionBatch{}, err
	}
	defer os.RemoveAll(tmpDir)

	schemaPath := filepath.Join(tmpDir, "ai-selection.schema.json")
	outputPath := filepath.Join(tmpDir, "ai-result.json")
	if err := os.WriteFile(schemaPath, []byte(aiOutputSchema), 0o600); err != nil {
		return AISelectionBatch{}, err
	}

	prompt, err := BuildAIPrompt(items)
	if err != nil {
		return AISelectionBatch{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		cfg.AI.CodexPath,
		"exec",
		"--model", cfg.AI.Model,
		"--sandbox", "read-only",
		"--output-schema", schemaPath,
		"--output-last-message", outputPath,
		"-",
	)
	cmd.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return AISelectionBatch{}, fmt.Errorf("codex exec failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		return AISelectionBatch{}, err
	}
	return ParseAISelection(data)
}

func BuildAIPrompt(items []AIItem) (string, error) {
	data, err := json.MarshalIndent(struct {
		Items []AIItem `json:"items"`
	}{Items: items}, "", "  ")
	if err != nil {
		return "", err
	}
	return `You select the dictionary sense that best matches each Kindle usage example.

Rules:
- Read the Kindle usage sentence and the candidate senseHtml values.
- Choose exactly one candidateID per item only when the context clearly matches it.
- Use confidence from 0 to 1.
- If uncertain, still choose the closest candidate but set low confidence.
- Return only JSON matching the provided schema.

Input:
` + string(data), nil
}

func ParseAISelection(data []byte) (AISelectionBatch, error) {
	text := strings.TrimSpace(string(data))
	var batch AISelectionBatch
	if err := json.Unmarshal([]byte(text), &batch); err == nil {
		return batch, nil
	}
	re := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
	if match := re.FindStringSubmatch(text); len(match) == 2 {
		if err := json.Unmarshal([]byte(match[1]), &batch); err == nil {
			return batch, nil
		}
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(text[start:end+1]), &batch); err == nil {
			return batch, nil
		}
	}
	return AISelectionBatch{}, errors.New("codex output did not contain valid selection JSON")
}

func findCandidate(candidates []FlashDictSenseCandidate, id string) (FlashDictSenseCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.CandidateID == id {
			return candidate, true
		}
	}
	return FlashDictSenseCandidate{}, false
}

func reviewFromSelection(record KindleRecord, candidates []FlashDictSenseCandidate, selection AISelection, reason, detail string) ReviewRecord {
	return ReviewRecord{
		RequestID:  record.RequestID,
		Term:       record.Term,
		Usage:      record.Usage,
		BookTitle:  record.BookTitle,
		Location:   record.Location,
		Reason:     reason,
		Candidates: candidates,
		AI:         &selection,
		Error:      detail,
	}
}

const aiOutputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["results"],
  "properties": {
    "results": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["requestID", "selectedCandidateID", "confidence", "reason"],
        "properties": {
          "requestID": {"type": "string"},
          "selectedCandidateID": {"type": "string"},
          "confidence": {"type": "number", "minimum": 0, "maximum": 1},
          "reason": {"type": "string"}
        }
      }
    }
  }
}`
