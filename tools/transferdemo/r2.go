package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type r2Backend struct {
	endpoint       *url.URL
	bucket         string
	accessKey      string
	secretKey      string
	client         *http.Client
	now            func() time.Time
	checksumSHA256 bool
}

func newR2Backend(endpoint, bucket, accessKey, secretKey string) (*r2Backend, error) {
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("R2 endpoint must be an absolute HTTPS URL")
	}
	if bucket == "" || strings.Contains(bucket, "/") {
		return nil, errors.New("invalid R2 bucket")
	}
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("R2 credentials are incomplete")
	}
	return &r2Backend{
		endpoint: parsed, bucket: bucket, accessKey: accessKey,
		secretKey: secretKey, client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (b *r2Backend) PresignPut(_ requestContext, objectID string, _ int64, contentType, sha string, expires time.Time) (authorizedURL, error) {
	headers := map[string]string{
		"content-type":      contentType,
		"x-amz-meta-sha256": sha,
	}
	responseHeaders := map[string]string{
		"Content-Type": contentType, "x-amz-meta-sha256": sha,
	}
	if b.checksumSHA256 {
		raw, err := hex.DecodeString(sha)
		if err != nil || len(raw) != sha256.Size {
			return authorizedURL{}, errors.New("invalid SHA-256 for R2 checksum")
		}
		checksum := base64.StdEncoding.EncodeToString(raw)
		headers["x-amz-checksum-sha256"] = checksum
		responseHeaders["x-amz-checksum-sha256"] = checksum
	}
	target, err := b.presign(http.MethodPut, objectID, headers, nil, expires)
	if err != nil {
		return authorizedURL{}, err
	}
	return authorizedURL{
		URL: target, Method: http.MethodPut,
		Headers: responseHeaders,
	}, nil
}

func (b *r2Backend) PresignGet(_ requestContext, objectID, filename string, expires time.Time) (authorizedURL, error) {
	query := url.Values{}
	query.Set("response-content-disposition", `attachment; filename="`+safeDownloadFilename(filename)+`"`)
	target, err := b.presign(http.MethodGet, objectID, nil, query, expires)
	if err != nil {
		return authorizedURL{}, err
	}
	return authorizedURL{URL: target, Method: http.MethodGet}, nil
}

func (b *r2Backend) Head(ctx requestContext, objectID string) (objectInfo, error) {
	response, err := b.signedRequest(ctx, http.MethodHead, objectID)
	if err != nil {
		return objectInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return objectInfo{}, fmt.Errorf("R2 HEAD returned %s", response.Status)
	}
	size, err := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return objectInfo{}, errors.New("R2 HEAD omitted a valid Content-Length")
	}
	return objectInfo{
		Size: size, ETag: response.Header.Get("ETag"),
		SHA256: response.Header.Get("x-amz-meta-sha256"),
	}, nil
}

func (b *r2Backend) Delete(ctx requestContext, objectID string) error {
	response, err := b.signedRequest(ctx, http.MethodDelete, objectID)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("R2 DELETE returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return nil
}

func (b *r2Backend) objectURL(objectID string) *url.URL {
	target := *b.endpoint
	target.Path = path.Join(target.Path, b.bucket, objectID)
	return &target
}

func (b *r2Backend) presign(method, objectID string, headers map[string]string, extra url.Values, expires time.Time) (string, error) {
	now := b.currentTime().UTC()
	seconds := int64(expires.Sub(now).Seconds())
	if seconds < 1 {
		return "", errors.New("R2 authorization has already expired")
	}
	if seconds > int64((7 * 24 * time.Hour).Seconds()) {
		seconds = int64((7 * 24 * time.Hour).Seconds())
	}
	target := b.objectURL(objectID)
	query := target.Query()
	for key, values := range extra {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	date := now.Format("20060102")
	timestamp := now.Format("20060102T150405Z")
	scope := date + "/auto/s3/aws4_request"
	signedHeaders, canonicalHeaders := canonicalHeaderBlock(target.Host, headers)
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", b.accessKey+"/"+scope)
	query.Set("X-Amz-Date", timestamp)
	query.Set("X-Amz-Expires", strconv.FormatInt(seconds, 10))
	query.Set("X-Amz-SignedHeaders", signedHeaders)
	target.RawQuery = canonicalQuery(query)
	canonicalRequest := strings.Join([]string{
		method, canonicalURI(target), canonicalQuery(target.Query()),
		canonicalHeaders, signedHeaders, "UNSIGNED-PAYLOAD",
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", timestamp, scope, hashHex([]byte(canonicalRequest)),
	}, "\n")
	query.Set("X-Amz-Signature", hex.EncodeToString(b.sign(date, stringToSign)))
	target.RawQuery = canonicalQuery(query)
	return target.String(), nil
}

func (b *r2Backend) signedRequest(ctx requestContext, method, objectID string) (*http.Response, error) {
	now := b.currentTime().UTC()
	target := b.objectURL(objectID)
	emptyHash := hashHex(nil)
	headers := map[string]string{
		"x-amz-content-sha256": emptyHash,
		"x-amz-date":           now.Format("20060102T150405Z"),
	}
	signedHeaders, canonicalHeaders := canonicalHeaderBlock(target.Host, headers)
	canonicalRequest := strings.Join([]string{
		method, canonicalURI(target), "", canonicalHeaders, signedHeaders, emptyHash,
	}, "\n")
	date := now.Format("20060102")
	scope := date + "/auto/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", headers["x-amz-date"], scope, hashHex([]byte(canonicalRequest)),
	}, "\n")
	authorization := "AWS4-HMAC-SHA256 Credential=" + b.accessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + hex.EncodeToString(b.sign(date, stringToSign))
	request, err := http.NewRequestWithContext(contextAdapter{ctx}, method, target.String(), bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	request.Header.Set("x-amz-date", headers["x-amz-date"])
	request.Header.Set("x-amz-content-sha256", emptyHash)
	request.Header.Set("Authorization", authorization)
	return b.client.Do(request)
}

func (b *r2Backend) sign(date, stringToSign string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+b.secretKey), date)
	kRegion := hmacSHA256(kDate, "auto")
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	return hmacSHA256(kSigning, stringToSign)
}

func (b *r2Backend) currentTime() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func canonicalHeaderBlock(host string, extra map[string]string) (string, string) {
	headers := map[string]string{"host": strings.TrimSpace(host)}
	for key, value := range extra {
		headers[strings.ToLower(strings.TrimSpace(key))] = strings.Join(strings.Fields(value), " ")
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var block strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&block, "%s:%s\n", key, headers[key])
	}
	return strings.Join(keys, ";"), block.String()
}

func canonicalURI(target *url.URL) string {
	escaped := target.EscapedPath()
	if escaped == "" {
		return "/"
	}
	return escaped
}

func canonicalQuery(query url.Values) string {
	type pair struct{ key, value string }
	var pairs []pair
	for key, values := range query {
		encodedKey := awsQueryEscape(key)
		for _, value := range values {
			pairs = append(pairs, pair{encodedKey, awsQueryEscape(value)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})
	var parts []string
	for _, item := range pairs {
		parts = append(parts, item.key+"="+item.value)
	}
	return strings.Join(parts, "&")
}

func awsQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func hashHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

// contextAdapter bridges the intentionally small adapter interface back to the
// standard request context without forcing fakes to import context.
type contextAdapter struct{ requestContext }

type r2Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func parseR2Error(payload []byte) string {
	var value r2Error
	if xml.Unmarshal(payload, &value) == nil && value.Code != "" {
		return value.Code + ": " + value.Message
	}
	return strings.TrimSpace(string(payload))
}
