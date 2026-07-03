package vocab

import (
	"strings"
	"testing"
)

func TestParseAISelectionAcceptsFencedJSON(t *testing.T) {
	data := []byte("```json\n{\"results\":[{\"requestID\":\"kindle-1\",\"selectedCandidateID\":\"c1\",\"confidence\":0.91,\"reason\":\"matches context\"}]}\n```")

	batch, err := ParseAISelection(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(batch.Results) != 1 {
		t.Fatalf("len(results) = %d", len(batch.Results))
	}
	result := batch.Results[0]
	if result.RequestID != "kindle-1" || result.SelectedCandidateID != "c1" || result.Confidence != 0.91 {
		t.Fatalf("unexpected result: %+v", result)
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
