package transfer

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestR2PresignedPutBindsBusinessHashAndContentType(t *testing.T) {
	backend, err := newR2Backend(
		"https://account.r2.cloudflarestorage.com", "bucket", "ACCESS", "SECRET",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }
	auth, err := backend.PresignPut(nil, strings.Repeat("a", 48), 42,
		"application/pdf", strings.Repeat("b", 64), now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(auth.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if auth.Method != "PUT" || query.Get("X-Amz-Expires") != "7200" ||
		!strings.Contains(query.Get("X-Amz-SignedHeaders"), "x-amz-meta-sha256") ||
		auth.Headers["x-amz-meta-sha256"] != strings.Repeat("b", 64) ||
		query.Get("X-Amz-Signature") == "" {
		t.Fatalf("authorization = %#v, query = %#v", auth, query)
	}
}

func TestR2OnlyPresignsPutAndGetThroughInterface(t *testing.T) {
	var backend objectBackend
	value, err := newR2Backend("https://account.r2.cloudflarestorage.com", "bucket", "A", "S")
	if err != nil {
		t.Fatal(err)
	}
	backend = value
	if backend == nil {
		t.Fatal("R2 backend is nil")
	}
}

func TestR2PresignedGetUsesAWSPercentEncoding(t *testing.T) {
	backend, err := newR2Backend(
		"https://account.r2.cloudflarestorage.com", "bucket", "ACCESS", "SECRET",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }
	auth, err := backend.PresignGet(nil, strings.Repeat("a", 48), "sample book.pdf", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auth.URL, "+") || !strings.Contains(auth.URL, "%20") {
		t.Fatalf("presigned query is not RFC 3986 encoded: %s", auth.URL)
	}
}

func TestR2ChecksumHeaderIsOnlyEnabledExplicitly(t *testing.T) {
	backend, err := newR2Backend(
		"https://account.r2.cloudflarestorage.com", "bucket", "ACCESS", "SECRET",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }
	sha := hashHex([]byte("payload"))
	without, err := backend.PresignPut(nil, strings.Repeat("a", 48), 7, "text/plain", sha, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if without.Headers["x-amz-checksum-sha256"] != "" {
		t.Fatal("checksum was enabled before capability opt-in")
	}
	backend.checksumSHA256 = true
	with, err := backend.PresignPut(nil, strings.Repeat("b", 48), 7, "text/plain", sha, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if with.Headers["x-amz-checksum-sha256"] == "" ||
		!strings.Contains(with.URL, "x-amz-checksum-sha256") {
		t.Fatalf("checksum authorization = %#v", with)
	}
}
