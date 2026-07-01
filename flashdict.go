package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func LookupFlashDictSenses(cfg Config, records []KindleRecord) (map[string]FlashDictLookupResponse, []ReviewRecord, int, error) {
	cmd := exec.Command(cfg.SenseSource.CLIPath, "lookup-senses", "--jsonl")
	var stdin bytes.Buffer
	for _, record := range records {
		request := FlashDictLookupRequest{
			RequestID: record.RequestID,
			Term:      record.Term,
			Usage:     record.Usage,
		}
		line, err := json.Marshal(request)
		if err != nil {
			return nil, nil, 0, err
		}
		stdin.Write(line)
		stdin.WriteByte('\n')
	}
	cmd.Stdin = &stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, 0, fmt.Errorf("flashdict-cli failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	byID := map[string]KindleRecord{}
	for _, record := range records {
		byID[record.RequestID] = record
	}
	responses := map[string]FlashDictLookupResponse{}
	var reviews []ReviewRecord
	lookupFailures := 0
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var response FlashDictLookupResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			return nil, nil, 0, fmt.Errorf("decode flashdict-cli response: %w", err)
		}
		record := byID[response.RequestID]
		if response.Status != "ok" || len(response.Candidates) == 0 {
			lookupFailures++
			reviews = append(reviews, ReviewRecord{
				RequestID: record.RequestID,
				Term:      record.Term,
				Usage:     record.Usage,
				BookTitle: record.BookTitle,
				Location:  record.Location,
				Reason:    "lookup_failed",
				Error:     response.Error,
			})
			continue
		}
		responses[response.RequestID] = response
	}
	return responses, reviews, lookupFailures, scanner.Err()
}
