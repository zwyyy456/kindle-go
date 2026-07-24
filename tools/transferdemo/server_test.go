package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignalSessionFlow(t *testing.T) {
	server := &Server{}
	handler := server.routes()

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/session", nil))
	if create.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %q", create.Code, create.Body.String())
	}
	var created struct {
		ID           string `json:"id"`
		ReceiverPath string `json:"receiver_path"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !validSessionID(created.ID) {
		t.Fatalf("session ID = %q", created.ID)
	}
	if want := "/receiver?id=" + created.ID; created.ReceiverPath != want {
		t.Fatalf("receiver path = %q, want %q", created.ReceiverPath, want)
	}

	offer := signalMessage{
		Session: created.ID,
		From:    "sender",
		Type:    "offer",
		Data:    json.RawMessage(`{"type":"offer","sdp":"test"}`),
	}
	body, err := json.Marshal(offer)
	if err != nil {
		t.Fatal(err)
	}
	post := httptest.NewRecorder()
	handler.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/signal", bytes.NewReader(body)))
	if post.Code != http.StatusOK {
		t.Fatalf("signal status = %d, body = %q", post.Code, post.Body.String())
	}

	pollReceiver := httptest.NewRecorder()
	handler.ServeHTTP(pollReceiver, httptest.NewRequest(
		http.MethodGet,
		"/api/poll?session="+created.ID+"&to=receiver&after=0",
		nil,
	))
	if pollReceiver.Code != http.StatusOK {
		t.Fatalf("receiver poll status = %d, body = %q", pollReceiver.Code, pollReceiver.Body.String())
	}
	var result struct {
		Messages []signalMessage `json:"messages"`
	}
	if err := json.Unmarshal(pollReceiver.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Seq != 1 || result.Messages[0].Type != "offer" {
		t.Fatalf("receiver messages = %#v", result.Messages)
	}

	pollSender := httptest.NewRecorder()
	handler.ServeHTTP(pollSender, httptest.NewRequest(
		http.MethodGet,
		"/api/poll?session="+created.ID+"&to=sender&after=0",
		nil,
	))
	if pollSender.Code != http.StatusOK {
		t.Fatalf("sender poll status = %d, body = %q", pollSender.Code, pollSender.Body.String())
	}
	if err := json.Unmarshal(pollSender.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("sender received its own messages: %#v", result.Messages)
	}
}

func TestSignalAPIRejectsInvalidRequests(t *testing.T) {
	handler := (&Server{}).routes()
	validSignalBody := `{"session":"123456","from":"sender","type":"offer","data":{"type":"offer"}}`

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
	}{
		{"unknown session", http.MethodPost, "/api/signal", validSignalBody, http.StatusNotFound},
		{"bad session", http.MethodPost, "/api/signal", `{"session":"123","from":"sender","type":"offer","data":{}}`, http.StatusBadRequest},
		{"bad sender type", http.MethodPost, "/api/signal", `{"session":"123456","from":"sender","type":"answer","data":{}}`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/api/signal", `{"session":"123456","from":"sender","type":"offer","data":{},"extra":true}`, http.StatusBadRequest},
		{"trailing JSON", http.MethodPost, "/api/signal", validSignalBody + `{}`, http.StatusBadRequest},
		{"bad poll recipient", http.MethodGet, "/api/poll?session=123456&to=other&after=0", "", http.StatusBadRequest},
		{"unknown poll session", http.MethodGet, "/api/poll?session=123456&to=sender&after=0", "", http.StatusNotFound},
		{"wrong create method", http.MethodGet, "/api/session", "", http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %q", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
}

func TestCapabilityReportIsLogged(t *testing.T) {
	var output bytes.Buffer
	handler := (&Server{Stdout: &output}).routes()
	body := `{
		"user_agent":"Kindle/legacy\nterminal-control",
		"secure_context":false,
		"lines":[
			"webkitPeerConnection00: function [createOffer, processIceMessage]",
			"RTCDataChannel: undefined"
		]
	}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/capabilities", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	got := output.String()
	if !strings.Contains(got, "webkitPeerConnection00: function") {
		t.Fatalf("output does not include legacy constructor: %q", got)
	}
	if strings.Contains(got, "legacy\nterminal") {
		t.Fatalf("output contains unsanitized newline: %q", got)
	}
}

func TestSignalAPILimitsRequestBody(t *testing.T) {
	handler := (&Server{}).routes()
	body := `{"session":"123456","from":"sender","type":"offer","data":{"sdp":"` +
		strings.Repeat("x", maxSignalBody) +
		`"}}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/signal", strings.NewReader(body)))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body = %q", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestSignalStoreExpiresInactiveSessions(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	store := signalStore{now: func() time.Time { return now }}
	if !store.create("123456") {
		t.Fatal("create session")
	}
	if _, ok := store.messagesFor("123456", "receiver", 0); !ok {
		t.Fatal("session should be active")
	}

	now = now.Add(defaultSessionTTL)
	if _, ok := store.messagesFor("123456", "receiver", 0); ok {
		t.Fatal("session should be expired")
	}
	if !store.create("123456") {
		t.Fatal("expired token should be reusable")
	}
}

func TestSignalStoreCannotSlideWaitingOrOnlineLifetime(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	store := signalStore{now: func() time.Time { return now }}
	if !store.create("123456") {
		t.Fatal("create session")
	}
	now = now.Add(9 * time.Minute)
	if _, ok := store.messagesFor("123456", "receiver", 0); !ok {
		t.Fatal("waiting session unexpectedly expired")
	}
	now = now.Add(time.Minute)
	if _, ok := store.messagesFor("123456", "receiver", 0); ok {
		t.Fatal("polling extended the ten-minute waiting lifetime")
	}

	now = time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC)
	if !store.create("654321") || !store.start("654321", 2*time.Hour) {
		t.Fatal("start online session")
	}
	now = now.Add(time.Hour)
	if !store.start("654321", 2*time.Hour) {
		t.Fatal("idempotent online start failed")
	}
	now = now.Add(time.Hour)
	if _, ok := store.messagesFor("654321", "receiver", 0); ok {
		t.Fatal("repeated start extended the two-hour online lifetime")
	}
}

func TestSignalStoreCapsMessagesPerSession(t *testing.T) {
	store := signalStore{}
	if !store.create("123456") {
		t.Fatal("create session")
	}
	for i := 0; i < maxSignalMessages; i++ {
		if _, ok := store.append(signalMessage{
			Session: "123456", From: "sender", Type: "offer", Data: json.RawMessage(`{}`),
		}); !ok {
			t.Fatalf("message %d rejected", i)
		}
	}
	if _, ok := store.append(signalMessage{
		Session: "123456", From: "sender", Type: "offer", Data: json.RawMessage(`{}`),
	}); ok {
		t.Fatal("message limit was not enforced")
	}
}

func TestHTTPPagesAndMethods(t *testing.T) {
	handler := (&Server{}).routes()
	tests := []struct {
		target      string
		contentType string
		contains    string
	}{
		{"/", "text/html", "Receive a file"},
		{"/sender", "text/html", "Send a file"},
		{"/receiver", "text/html", "Receive a file"},
		{"/diagnostics", "text/html", "Compatibility diagnostics"},
		{"/sw.js", "application/javascript", `addEventListener("fetch"`},
		{"/sha256-worker.js", "application/javascript", `kind==="finish"`},
		{"/http-sample.txt", "text/plain", "plain HTTP"},
	}
	for _, test := range tests {
		t.Run(test.target, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.target, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, test.contentType) {
				t.Fatalf("content type = %q, want prefix %q", got, test.contentType)
			}
			if !strings.Contains(recorder.Body.String(), test.contains) {
				t.Fatalf("body does not contain %q", test.contains)
			}
		})
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/sender", nil))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST /sender status = %d, Allow = %q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

func TestTransferPagesContainOfflineAndBoundedOnlinePaths(t *testing.T) {
	for _, expected := range []string{
		`value="ONLINE"`, `value="R2"`, `value="SERVER_A"`,
		`/api/transfers`, `upload-started`, `upload-authorize`,
		`file.slice`, `Math.min(32768`,
		`pc.sctp.maxMessageSize`, `bufferedAmountLowThreshold`,
		`bufferedamountlow`, `new Worker("/sha256-worker.js")`,
		`getStats`, `candidateType === "relay"`,
	} {
		if !strings.Contains(senderHTML, expected) {
			t.Errorf("sender page does not contain %q", expected)
		}
	}
	if strings.Contains(senderHTML, "file.arrayBuffer()") {
		t.Error("sender reads the complete file into memory")
	}
	for _, expected := range []string{
		`/api/receive/`, `No ready offline file was found`,
		`showSaveFilePicker`, `receivedChunks`, `expectedDone.sha256`,
		`new Worker("/sha256-worker.js")`, `getStats`,
	} {
		if !strings.Contains(receiverHTML, expected) {
			t.Errorf("receiver page does not contain %q", expected)
		}
	}
}

func TestClientConfigRejectsTURN(t *testing.T) {
	if _, err := parseSTUNURLs("turn:relay.example.com"); err == nil {
		t.Fatal("TURN URL accepted")
	}
	got, err := parseSTUNURLs("stun:one.example.com, stuns:two.example.com")
	if err != nil || len(got) != 2 {
		t.Fatalf("STUN URLs = %#v, err = %v", got, err)
	}
}

func TestPagesProbeLegacyWebRTCAPI(t *testing.T) {
	for name, page := range map[string]string{
		"sender":   senderHTML,
		"receiver": receiverHTML,
	} {
		t.Run(name, func(t *testing.T) {
			for _, expected := range []string{
				"webkitPeerConnection00",
				"webkitPeerConnection",
				"PeerConnection",
				"createDataChannel",
				"processIceMessage",
				"startIce",
				"binary payload was coerced to text",
			} {
				if !strings.Contains(page, expected) {
					t.Errorf("page does not probe %q", expected)
				}
			}
		})
	}
}

func TestAddressHelpers(t *testing.T) {
	if host := hostWithoutPort("192.168.1.2:8790"); host != "192.168.1.2" {
		t.Fatalf("host = %q", host)
	}
	if host := hostWithoutPort("[fe80::1]:8790"); host != "fe80::1" {
		t.Fatalf("IPv6 host = %q", host)
	}
	got := urls("127.0.0.1:8790")
	if len(got) != 1 || got[0] != "http://127.0.0.1:8790/" {
		t.Fatalf("urls = %#v", got)
	}
}
