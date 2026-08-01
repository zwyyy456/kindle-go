package vocab

import (
	"strings"
	"testing"
)

func TestParseAISelectionRejectsFencedJSON(t *testing.T) {
	data := []byte("```json\n{\"results\":[{\"requestID\":\"kindle-1\",\"selectedCandidateID\":\"c1\",\"confidence\":0.91,\"reason\":\"matches context\"}]}\n```")

	if _, err := ParseAISelection(data); err == nil {
		t.Fatal("ParseAISelection accepted fenced JSON")
	}
}

func TestParseAISelectionRejectsInvalidOutput(t *testing.T) {
	if _, err := ParseAISelection([]byte("not json")); err == nil {
		t.Fatal("ParseAISelection returned nil error")
	}
}

func TestBuildAIPromptIncludesItems(t *testing.T) {
	prompt, err := BuildAIPrompt([]AIItem{{
		RequestID: "kindle-1",
		Term:      "example",
		Usage:     "An example sentence.",
		Candidates: []AICandidate{{
			CandidateID: "c1",
			SenseHTML:   "<div>example</div>",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"kindle-1", "example", "An example sentence.", "c1"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestValidateSelectionBatchRequiresExactRequestIDs(t *testing.T) {
	items := []AIItem{{RequestID: "kindle-1"}, {RequestID: "kindle-2"}}
	tests := []struct {
		name    string
		results []AISelection
		want    string
	}{
		{name: "missing", results: []AISelection{{RequestID: "kindle-1"}}, want: "missing requestID"},
		{name: "duplicate", results: []AISelection{{RequestID: "kindle-1"}, {RequestID: "kindle-1"}}, want: "duplicate requestID"},
		{name: "unknown", results: []AISelection{{RequestID: "kindle-1"}, {RequestID: "kindle-3"}}, want: "unknown requestID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSelectionBatch(items, AISelectionBatch{Results: test.results}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateSelectionBatch error = %v", err)
			}
		})
	}
}
