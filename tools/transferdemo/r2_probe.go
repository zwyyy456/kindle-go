package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func runR2Probe(args []string) error {
	fs := flag.NewFlagSet("transferdemo r2-probe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	endpoint := fs.String("endpoint", "", "R2 S3 endpoint")
	bucket := fs.String("bucket", "", "R2 bucket")
	accessKey := fs.String("access-key-id", "", "R2 access key ID")
	secretKey := fs.String("secret-access-key", "", "R2 secret access key")
	origin := fs.String("origin", "", "browser origin to verify against bucket CORS")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	backend, err := newR2Backend(*endpoint, *bucket, *accessKey, *secretKey)
	if err != nil {
		return err
	}
	payload := []byte("transferdemo R2 acceptance " + time.Now().UTC().Format(time.RFC3339Nano))
	sum := sha256.Sum256(payload)
	shaHex := fmt.Sprintf("%x", sum[:])
	checksumB64 := base64.StdEncoding.EncodeToString(sum[:])
	objectID, err := randomHex(objectIDN)
	if err != nil {
		return err
	}
	fmt.Printf("object: %s\n", objectID)

	auth, err := backend.PresignPut(context.Background(), objectID, int64(len(payload)),
		"text/plain", shaHex, time.Now().Add(10*time.Minute))
	if err != nil {
		return err
	}
	if *origin != "" {
		if err := probeR2CORS(auth.URL, *origin, "content-type, x-amz-meta-sha256"); err != nil {
			return err
		}
		fmt.Println("CORS preflight: success")
	}
	etag, err := putProbeObject(backend.client, auth, payload, *origin)
	if err != nil {
		return err
	}
	info, err := backend.Head(context.Background(), objectID)
	if err != nil {
		return err
	}
	if info.Size != int64(len(payload)) || info.SHA256 != shaHex || normalizeETag(info.ETag) != normalizeETag(etag) {
		return fmt.Errorf("HEAD mismatch: size=%d etag=%q metadata-sha256=%q", info.Size, info.ETag, info.SHA256)
	}
	fmt.Println("baseline PUT + HEAD: success")
	if err := probeR2Range(backend, objectID, payload); err != nil {
		return err
	}
	fmt.Println("GET Range: success")
	if err := backend.Delete(context.Background(), objectID); err != nil {
		return err
	}
	fmt.Println("C-side DELETE: success")

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
	if *origin != "" {
		if err := probeR2CORS(checksumURL, *origin, "content-type, x-amz-meta-sha256, x-amz-checksum-sha256"); err != nil {
			fmt.Printf("checksum CORS capability: unavailable (%v)\n", err)
		}
	}
	checksumETag, checksumErr := putProbeObject(backend.client, checksumAuth, payload, *origin)
	if checksumErr != nil {
		fmt.Printf("x-amz-checksum-sha256 capability: unavailable (%v)\n", checksumErr)
		return nil
	}
	checksumInfo, headErr := backend.Head(context.Background(), checksumObject)
	if headErr != nil {
		return headErr
	}
	fmt.Printf("x-amz-checksum-sha256 capability: accepted; ETag=%s; HEAD metadata SHA-256=%s\n",
		checksumETag, checksumInfo.SHA256)
	_ = backend.Delete(context.Background(), checksumObject)

	badObject, _ := randomHex(objectIDN)
	badURL, err := backend.presign(http.MethodPut, badObject, checksumHeaders, nil, time.Now().Add(10*time.Minute))
	if err != nil {
		return err
	}
	badAuth := checksumAuth
	badAuth.URL = badURL
	if _, err := putProbeObject(backend.client, badAuth, append(payload, '!'), *origin); err == nil {
		_ = backend.Delete(context.Background(), badObject)
		return fmt.Errorf("R2 accepted a body that did not match x-amz-checksum-sha256")
	}
	fmt.Println("BadDigest behavior: success (mismatched body rejected)")
	return nil
}

func putProbeObject(client *http.Client, auth authorizedURL, payload []byte, origin string) (string, error) {
	request, err := http.NewRequest(http.MethodPut, auth.URL, bytes.NewReader(payload))
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

func probeR2CORS(target, origin, headers string) error {
	request, err := http.NewRequest(http.MethodOptions, target, nil)
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

func probeR2Range(backend *r2Backend, objectID string, payload []byte) error {
	auth, err := backend.PresignGet(context.Background(), objectID, "probe.txt", time.Now().Add(10*time.Minute))
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodGet, auth.URL, nil)
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
