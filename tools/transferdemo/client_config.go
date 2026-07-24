package main

import (
	"errors"
	"strings"
)

func parseSTUNURLs(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		lower := strings.ToLower(item)
		if strings.HasPrefix(lower, "turn:") || strings.HasPrefix(lower, "turns:") {
			return nil, errors.New("TURN is intentionally unsupported")
		}
		if !strings.HasPrefix(lower, "stun:") && !strings.HasPrefix(lower, "stuns:") {
			return nil, errors.New("only STUN URLs are allowed")
		}
		out = append(out, item)
	}
	return out, nil
}
