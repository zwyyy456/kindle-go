package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// R2ProbeConfig configures the explicit real-service acceptance probe.
type R2ProbeConfig struct {
	R2Config
	Origin string
	Stdout io.Writer
}

// ProbeR2 verifies the configured R2 data plane, including CORS, Range, metadata, and deletion.
func ProbeR2(ctx context.Context, config R2ProbeConfig) error {
	backend, err := newR2Backend(config.Endpoint, config.Bucket, config.AccessKeyID, config.SecretAccessKey)
	if err != nil {
		return err
	}
	out := config.Stdout
	if out == nil {
		out = io.Discard
	}
	payload := []byte("transfer R2 acceptance " + time.Now().UTC().Format(time.RFC3339Nano))
	sum := sha256.Sum256(payload)
	shaHex := fmt.Sprintf("%x", sum[:])
	checksumB64 := base64.StdEncoding.EncodeToString(sum[:])
	objectID, err := randomHex(objectIDN)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "object: %s\n", objectID)

	auth, err := backend.PresignPut(ctx, objectID, int64(len(payload)),
		"text/plain", shaHex, time.Now().Add(10*time.Minute))
	if err != nil {
		return err
	}
	if config.Origin != "" {
		if err := probeR2CORS(ctx, auth.URL, config.Origin, "content-type, x-amz-meta-sha256"); err != nil {
			return err
		}
		fmt.Fprintln(out, "CORS preflight: success")
	}
	etag, err := putProbeObject(ctx, backend.client, auth, payload, config.Origin)
	if err != nil {
		return err
	}
	info, err := backend.Head(ctx, objectID)
	if err != nil {
		return err
	}
	if info.Size != int64(len(payload)) || info.SHA256 != shaHex || normalizeETag(info.ETag) != normalizeETag(etag) {
		return fmt.Errorf("HEAD mismatch: size=%d etag=%q metadata-sha256=%q", info.Size, info.ETag, info.SHA256)
	}
	fmt.Fprintln(out, "baseline PUT + HEAD: success")
	if err := probeR2Range(ctx, backend, objectID, payload); err != nil {
		return err
	}
	fmt.Fprintln(out, "GET Range: success")
	if err := backend.Delete(ctx, objectID); err != nil {
		return err
	}
	fmt.Fprintln(out, "C-side DELETE: success")

	checksumObject, _ := randomHex(objectIDN)
	checksumHeaders := map[string]string{
		"content-type":          "text/plain",
		"x-amz-meta-sha256":     shaHex,
		"x-amz-checksum-sha256": checksumB64,
	}
	checksumURL, err := backend.presign(http.MethodPut, checksumObject, checksumHeaders, nil, time.Now().Add(10*time.Minute))
	if err != nil {
		return err
	}
	checksumAuth := authorizedURL{
		URL: checksumURL, Method: http.MethodPut,
		Headers: map[string]string{
			"Content-Type": "text/plain", "x-amz-meta-sha256": shaHex,
			"x-amz-checksum-sha256": checksumB64,
		},
	}
	if config.Origin != "" {
		if err := probeR2CORS(ctx, checksumURL, config.Origin, "content-type, x-amz-meta-sha256, x-amz-checksum-sha256"); err != nil {
			fmt.Fprintf(out, "checksum CORS capability: unavailable (%v)\n", err)
		}
	}
	checksumETag, checksumErr := putProbeObject(ctx, backend.client, checksumAuth, payload, config.Origin)
	if checksumErr != nil {
		fmt.Fprintf(out, "x-amz-checksum-sha256 capability: unavailable (%v)\n", checksumErr)
		return nil
	}
	checksumInfo, headErr := backend.Head(ctx, checksumObject)
	if headErr != nil {
		return headErr
	}
	fmt.Fprintf(out, "x-amz-checksum-sha256 capability: accepted; ETag=%s; HEAD metadata SHA-256=%s\n",
		checksumETag, checksumInfo.SHA256)
	_ = backend.Delete(ctx, checksumObject)

	badObject, _ := randomHex(objectIDN)
	badURL, err := backend.presign(http.MethodPut, badObject, checksumHeaders, nil, time.Now().Add(10*time.Minute))
	if err != nil {
		return err
	}
	badAuth := checksumAuth
	badAuth.URL = badURL
	if _, err := putProbeObject(ctx, backend.client, badAuth, append(payload, '!'), config.Origin); err == nil {
		_ = backend.Delete(ctx, badObject)
		return fmt.Errorf("R2 accepted a body that did not match x-amz-checksum-sha256")
	}
	fmt.Fprintln(out, "BadDigest behavior: success (mismatched body rejected)")
	return nil
}

func putProbeObject(ctx context.Context, client *http.Client, auth authorizedURL, payload []byte, origin string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, auth.URL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	for key, value := range auth.Headers {
		request.Header.Set(key, value)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("PUT returned %s: %s", response.Status, parseR2Error(body))
	}
	if origin != "" && response.Header.Get("Access-Control-Allow-Origin") != origin {
		return "", fmt.Errorf("PUT response did not allow origin %q", origin)
	}
	if origin != "" && !headerListContains(response.Header.Get("Access-Control-Expose-Headers"), "etag") {
		return "", fmt.Errorf("PUT response does not expose ETag")
	}
	return response.Header.Get("ETag"), nil
}

func probeR2CORS(ctx context.Context, target, origin, headers string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodOptions, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPut)
	request.Header.Set("Access-Control-Request-Headers", headers)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OPTIONS returned %s", response.Status)
	}
	if response.Header.Get("Access-Control-Allow-Origin") != origin ||
		!strings.Contains(strings.ToUpper(response.Header.Get("Access-Control-Allow-Methods")), "PUT") {
		return fmt.Errorf("CORS policy did not allow origin/method")
	}
	for _, requested := range strings.Split(headers, ",") {
		if !headerListContains(response.Header.Get("Access-Control-Allow-Headers"), strings.TrimSpace(requested)) {
			return fmt.Errorf("CORS policy did not allow header %q", strings.TrimSpace(requested))
		}
	}
	return nil
}

func headerListContains(value, wanted string) bool {
	for _, item := range strings.Split(strings.ToLower(value), ",") {
		if strings.TrimSpace(item) == strings.ToLower(wanted) || strings.TrimSpace(item) == "*" {
			return true
		}
	}
	return false
}

func probeR2Range(ctx context.Context, backend *r2Backend, objectID string, payload []byte) error {
	auth, err := backend.PresignGet(ctx, objectID, "probe.txt", time.Now().Add(10*time.Minute))
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, auth.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Range", "bytes=0-3")
	response, err := backend.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	got, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusPartialContent || !bytes.Equal(got, payload[:4]) {
		return fmt.Errorf("Range returned %s and %q", response.Status, got)
	}
	return nil
}
