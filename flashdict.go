package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

func LookupFlashDictSenses(cfg Config, records []KindleRecord) (map[string]FlashDictLookupResponse, []ReviewRecord, int, error) {
	socketPath, err := resolveLookupBridgeSocketPath(cfg)
	if err != nil {
		return nil, nil, 0, err
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("connect FlashDict lookup bridge at %s: %w; is FlashDict running?", socketPath, err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	encoder.SetEscapeHTML(false)
	for _, record := range records {
		request := FlashDictLookupRequest{
			RequestID: record.RequestID,
			Term:      record.Term,
			Usage:     record.Usage,
		}
		if err := encoder.Encode(request); err != nil {
			return nil, nil, 0, err
		}
	}
	if unixConn, ok := conn.(*net.UnixConn); ok {
		if err := unixConn.CloseWrite(); err != nil {
			return nil, nil, 0, err
		}
	}

	byID := map[string]KindleRecord{}
	for _, record := range records {
		byID[record.RequestID] = record
	}
	responses := map[string]FlashDictLookupResponse{}
	var reviews []ReviewRecord
	lookupFailures := 0
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var response FlashDictLookupResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			return nil, nil, 0, fmt.Errorf("decode FlashDict lookup bridge response: %w", err)
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

type lookupBridgeDiscovery struct {
	Version    int    `json:"version"`
	Transport  string `json:"transport"`
	SocketPath string `json:"socketPath"`
}

func resolveLookupBridgeSocketPath(cfg Config) (string, error) {
	if cfg.SenseSource.SocketPath != "" {
		return cfg.SenseSource.SocketPath, nil
	}

	data, err := os.ReadFile(cfg.SenseSource.DiscoveryPath)
	if err != nil {
		return "", fmt.Errorf("read FlashDict lookup bridge discovery file at %s: %w; is FlashDict running?", cfg.SenseSource.DiscoveryPath, err)
	}

	var discovery lookupBridgeDiscovery
	if err := json.Unmarshal(data, &discovery); err != nil {
		return "", fmt.Errorf("decode FlashDict lookup bridge discovery file: %w", err)
	}
	if discovery.Transport != "unix-socket" {
		return "", fmt.Errorf("unsupported FlashDict lookup bridge transport: %s", discovery.Transport)
	}
	if discovery.SocketPath == "" {
		return "", fmt.Errorf("FlashDict lookup bridge discovery file has empty socketPath")
	}
	return discovery.SocketPath, nil
}
