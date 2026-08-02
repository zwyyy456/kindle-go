package vocab

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupFlashDictSensesRecordsMissingResponses(t *testing.T) {
	socketDir, err := os.MkdirTemp("/tmp", "vocab-lookup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "lookup.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		decoder := json.NewDecoder(conn)
		var requests [2]FlashDictLookupRequest
		for index := range requests {
			if err := decoder.Decode(&requests[index]); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- json.NewEncoder(conn).Encode(FlashDictLookupResponse{
			RequestID: requests[0].RequestID,
			Status:    "ok",
			Candidates: []FlashDictSenseCandidate{{
				CandidateID: "candidate-1",
			}},
		})
	}()

	records := []KindleRecord{
		{RequestID: "request-1", Term: "alpha", Usage: "first usage", BookTitle: "Book A", Location: "10"},
		{RequestID: "request-2", Term: "beta", Usage: "second usage", BookTitle: "Book B", Location: "20"},
	}
	responses, reviews, failures, err := LookupFlashDictSenses(Config{
		SenseSource: SenseSourceConfig{SocketPath: socketPath},
	}, records)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 || responses["request-1"].RequestID != "request-1" {
		t.Fatalf("responses = %#v", responses)
	}
	if failures != 1 || len(reviews) != 1 {
		t.Fatalf("failures = %d, reviews = %#v", failures, reviews)
	}
	missing := reviews[0]
	if missing.RequestID != "request-2" || missing.Term != "beta" ||
		missing.Reason != "lookup_missing_response" || missing.Error == "" {
		t.Fatalf("missing response review = %#v", missing)
	}
}
