package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultStorageAddr = ":8791"
	storageTokenMax    = 4096
)

type storageClaims struct {
	Version      int    `json:"v"`
	Method       string `json:"m"`
	ObjectID     string `json:"o"`
	ExpectedSize int64  `json:"s"`
	ExpiresAt    int64  `json:"e"`
	Nonce        string `json:"n"`
	Filename     string `json:"f,omitempty"`
}

type storageMetadata struct {
	ObjectID     string        `json:"object_id"`
	State        transferState `json:"state"`
	ExpectedSize int64         `json:"expected_size"`
	ActualSize   int64         `json:"actual_size"`
	SHA256       string        `json:"sha256"`
	UploadNonce  string        `json:"upload_nonce"`
	ExpiresAt    int64         `json:"expires_at"`
	UpdatedAt    int64         `json:"updated_at"`
}

type StorageServer struct {
	Addr          string
	Dir           string
	AllowedOrigin string
	Secret        []byte
	MaxDownloads  int
	Now           func() time.Time

	mu          sync.Mutex
	objectLocks map[string]*sync.Mutex
	downloads   atomic.Int64
}

func runStorage(args []string) error {
	fs := flag.NewFlagSet("transferdemo storage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", defaultStorageAddr, "address for storage server A")
	dir := fs.String("dir", "transfer-storage-data", "storage data directory")
	origin := fs.String("allowed-origin", "", "exact HTTPS origin of control server C")
	secret := fs.String("secret", "", "shared HMAC secret")
	maxDownloads := fs.Int("max-downloads", 3, "maximum concurrent GET/Range downloads")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: go run ./tools/transferdemo storage [options]")
	}
	if len(*secret) < 32 {
		return errors.New("-secret must contain at least 32 bytes")
	}
	if *origin == "" {
		return errors.New("-allowed-origin is required")
	}
	s := &StorageServer{
		Addr: *addr, Dir: *dir, AllowedOrigin: *origin, Secret: []byte(*secret),
		MaxDownloads: *maxDownloads,
	}
	return s.Run()
}

func (s *StorageServer) Run() error {
	if err := s.prepare(); err != nil {
		return err
	}
	stopCleanup := make(chan struct{})
	defer close(stopCleanup)
	go s.runFileCleanup(stopCleanup)
	server := &http.Server{
		Addr: s.Addr, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Minute, WriteTimeout: 15 * time.Minute,
		IdleTimeout: 60 * time.Second,
	}
	return server.ListenAndServe()
}

func (s *StorageServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/", s.handleUpload)
	mux.HandleFunc("/download/", s.handleDownload)
	mux.HandleFunc("/objects/", s.handleDelete)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethod(w, r, http.MethodGet) {
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "ok\n")
	})
	return mux
}

func (s *StorageServer) prepare() error {
	if len(s.Secret) < 32 {
		return errors.New("storage secret must contain at least 32 bytes")
	}
	if s.MaxDownloads <= 0 {
		s.MaxDownloads = 3
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}
	entries, err := filepath.Glob(filepath.Join(s.Dir, "*.part"))
	if err != nil {
		return err
	}
	for _, filename := range entries {
		// A .part is never a valid downloadable object. There is no supported
		// upload resume operation, so every remnant is safe to discard.
		if err := os.Remove(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove orphan upload %s: %w", filename, err)
		}
	}
	return s.cleanupExpiredFiles()
}

func (s *StorageServer) runFileCleanup(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_ = s.cleanupExpiredFiles()
		}
	}
}

func (s *StorageServer) cleanupExpiredFiles() error {
	entries, err := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	if err != nil {
		return err
	}
	now := s.currentTime().Unix()
	for _, filename := range entries {
		payload, err := os.ReadFile(filename)
		if err != nil {
			continue
		}
		var meta storageMetadata
		if json.Unmarshal(payload, &meta) != nil || !validObjectID(meta.ObjectID) {
			continue
		}
		if meta.ExpiresAt <= 0 || now < meta.ExpiresAt ||
			(meta.State != stateReady && meta.State != stateFailed && meta.State != stateUploading) {
			continue
		}
		lock := s.objectLock(meta.ObjectID)
		lock.Lock()
		_ = os.Remove(s.objectPath(meta.ObjectID))
		_ = os.Remove(s.partPath(meta.ObjectID))
		meta.State, meta.UpdatedAt = stateExpired, now
		_ = s.writeMetadata(meta)
		lock.Unlock()
	}
	return nil
}

func (s *StorageServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleUploadPreflight(w, r)
		return
	}
	if !allowMethod(w, r, http.MethodPut) {
		return
	}
	if !s.allowOrigin(w, r) {
		return
	}
	claims, err := s.parseClaims(strings.TrimPrefix(r.URL.Path, "/upload/"), http.MethodPut)
	if err != nil {
		http.Error(w, "invalid or expired upload authorization", http.StatusForbidden)
		return
	}
	if claims.ExpectedSize < 0 || claims.ExpectedSize > maxFileSize {
		http.Error(w, "file is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if declared, ok := contentLength(r); ok && declared > maxFileSize {
		http.Error(w, "file is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if declared, ok := contentLength(r); ok && declared != claims.ExpectedSize {
		http.Error(w, "upload size does not match authorization", http.StatusBadRequest)
		return
	}
	lock := s.objectLock(claims.ObjectID)
	lock.Lock()
	defer lock.Unlock()

	meta, metaErr := s.readMetadata(claims.ObjectID)
	if metaErr == nil && (meta.State == stateReady || meta.UploadNonce == claims.Nonce) {
		http.Error(w, "object already uploaded or authorization already used", http.StatusConflict)
		return
	}
	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		http.Error(w, "storage metadata is unavailable", http.StatusInternalServerError)
		return
	}
	now := s.currentTime().Unix()
	meta = storageMetadata{
		ObjectID: claims.ObjectID, State: stateUploading,
		ExpectedSize: claims.ExpectedSize, UploadNonce: claims.Nonce,
		ExpiresAt: claims.ExpiresAt, UpdatedAt: now,
	}
	if err := s.writeMetadata(meta); err != nil {
		http.Error(w, "cannot reserve upload", http.StatusInternalServerError)
		return
	}

	partPath := s.partPath(claims.ObjectID)
	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		meta.State, meta.UpdatedAt = stateFailed, s.currentTime().Unix()
		_ = s.writeMetadata(meta)
		http.Error(w, "cannot create upload", http.StatusConflict)
		return
	}
	hash := sha256.New()
	limited := http.MaxBytesReader(w, r.Body, maxFileSize+1)
	n, copyErr := io.Copy(io.MultiWriter(file, hash), limited)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || n != claims.ExpectedSize {
		_ = os.Remove(partPath)
		meta.State, meta.ActualSize, meta.UpdatedAt = stateFailed, n, s.currentTime().Unix()
		_ = s.writeMetadata(meta)
		if n > maxFileSize {
			http.Error(w, "file is too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "upload size does not match authorization", http.StatusBadRequest)
		}
		return
	}
	if err := os.Rename(partPath, s.objectPath(claims.ObjectID)); err != nil {
		_ = os.Remove(partPath)
		meta.State, meta.UpdatedAt = stateFailed, s.currentTime().Unix()
		_ = s.writeMetadata(meta)
		http.Error(w, "cannot commit upload", http.StatusInternalServerError)
		return
	}
	if err := syncDirectory(s.Dir); err != nil {
		_ = os.Remove(s.objectPath(claims.ObjectID))
		meta.State, meta.UpdatedAt = stateFailed, s.currentTime().Unix()
		_ = s.writeMetadata(meta)
		http.Error(w, "cannot commit upload", http.StatusInternalServerError)
		return
	}
	meta.State, meta.ActualSize, meta.SHA256, meta.UpdatedAt =
		stateReady, n, hex.EncodeToString(hash.Sum(nil)), s.currentTime().Unix()
	if err := s.writeMetadata(meta); err != nil {
		_ = os.Remove(s.objectPath(claims.ObjectID))
		http.Error(w, "cannot finalize upload", http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", `"`+meta.SHA256+`"`)
	w.Header().Set("X-Content-SHA256", meta.SHA256)
	w.WriteHeader(http.StatusCreated)
}

func (s *StorageServer) handleUploadPreflight(w http.ResponseWriter, r *http.Request) {
	if !s.allowOrigin(w, r) {
		return
	}
	requested := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
	if requested != http.MethodPut {
		http.Error(w, "CORS method not allowed", http.StatusForbidden)
		return
	}
	headers := strings.ToLower(r.Header.Get("Access-Control-Request-Headers"))
	for _, header := range strings.Split(headers, ",") {
		header = strings.TrimSpace(header)
		if header != "" && header != "content-type" {
			http.Error(w, "CORS header not allowed", http.StatusForbidden)
			return
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", "PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (s *StorageServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	claims, err := s.parseClaims(strings.TrimPrefix(r.URL.Path, "/download/"), http.MethodGet)
	if err != nil {
		http.Error(w, "invalid or expired download authorization", http.StatusForbidden)
		return
	}
	meta, err := s.readMetadata(claims.ObjectID)
	if err != nil || meta.State != stateReady || s.currentTime().Unix() >= meta.ExpiresAt {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		current := s.downloads.Add(1)
		defer s.downloads.Add(-1)
		if current > int64(s.MaxDownloads) {
			w.Header().Set("Retry-After", "10")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, "<!doctype html><meta charset=utf-8><title>线路繁忙</title><p>线路繁忙，请稍后重新点击下载。</p>")
			return
		}
	}
	file, err := os.Open(s.objectPath(claims.ObjectID))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "cannot read object", http.StatusInternalServerError)
		return
	}
	filename := safeDownloadFilename(claims.Filename)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-SHA256", meta.SHA256)
	w.Header().Set("ETag", `"`+meta.SHA256+`"`)
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func (s *StorageServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodDelete) {
		return
	}
	claims, err := s.parseClaims(strings.TrimPrefix(r.URL.Path, "/objects/"), http.MethodDelete)
	if err != nil {
		http.Error(w, "invalid or expired delete authorization", http.StatusForbidden)
		return
	}
	lock := s.objectLock(claims.ObjectID)
	lock.Lock()
	defer lock.Unlock()
	_ = os.Remove(s.objectPath(claims.ObjectID))
	_ = os.Remove(s.partPath(claims.ObjectID))
	meta, readErr := s.readMetadata(claims.ObjectID)
	if readErr == nil {
		meta.State, meta.UpdatedAt = stateRevoked, s.currentTime().Unix()
		if err := s.writeMetadata(meta); err != nil {
			http.Error(w, "cannot revoke object", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *StorageServer) allowOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin != s.AllowedOrigin {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", s.AllowedOrigin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Expose-Headers", "ETag, X-Content-SHA256")
	return true
}

func (s *StorageServer) parseClaims(token, method string) (storageClaims, error) {
	if len(token) == 0 || len(token) > storageTokenMax {
		return storageClaims{}, errors.New("invalid token length")
	}
	claims, err := verifyStorageClaims(token, s.Secret)
	if err != nil {
		return storageClaims{}, err
	}
	if claims.Version != 1 || claims.Method != method || !validObjectID(claims.ObjectID) ||
		s.currentTime().Unix() >= claims.ExpiresAt || claims.ExpiresAt <= 0 ||
		len(claims.Nonce) != 32 {
		return storageClaims{}, errors.New("invalid claims")
	}
	return claims, nil
}

func signStorageClaims(claims storageClaims, secret []byte) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	encoding := base64.RawURLEncoding
	return encoding.EncodeToString(payload) + "." + encoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyStorageClaims(token string, secret []byte) (storageClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return storageClaims{}, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return storageClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return storageClaims{}, err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return storageClaims{}, errors.New("invalid signature")
	}
	var claims storageClaims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return storageClaims{}, err
	}
	return claims, nil
}

func (s *StorageServer) readMetadata(objectID string) (storageMetadata, error) {
	payload, err := os.ReadFile(s.metadataPath(objectID))
	if err != nil {
		return storageMetadata{}, err
	}
	var meta storageMetadata
	if err := json.Unmarshal(payload, &meta); err != nil {
		return storageMetadata{}, err
	}
	return meta, nil
}

func (s *StorageServer) writeMetadata(meta storageMetadata) error {
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp := s.metadataPath(meta.ObjectID) + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(payload); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.metadataPath(meta.ObjectID)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDirectory(s.Dir)
}

func (s *StorageServer) objectLock(objectID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objectLocks == nil {
		s.objectLocks = make(map[string]*sync.Mutex)
	}
	if s.objectLocks[objectID] == nil {
		s.objectLocks[objectID] = &sync.Mutex{}
	}
	return s.objectLocks[objectID]
}

func (s *StorageServer) currentTime() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *StorageServer) objectPath(id string) string   { return filepath.Join(s.Dir, id+".data") }
func (s *StorageServer) partPath(id string) string     { return filepath.Join(s.Dir, id+".part") }
func (s *StorageServer) metadataPath(id string) string { return filepath.Join(s.Dir, id+".json") }

func validObjectID(id string) bool {
	if len(id) != objectIDN*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func safeDownloadFilename(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." {
		return "download"
	}
	if len(name) > 180 {
		name = name[:180]
	}
	return name
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func contentLength(r *http.Request) (int64, bool) {
	value := r.Header.Get("Content-Length")
	if value == "" {
		return 0, false
	}
	size, err := strconv.ParseInt(value, 10, 64)
	return size, err == nil
}
