package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

type createTransferRequest struct {
	Mode         transferMode `json:"mode"`
	Filename     string       `json:"filename"`
	ContentType  string       `json:"content_type"`
	ExpectedSize int64        `json:"expected_size"`
	SHA256       string       `json:"sha256"`
}

type managedRequest struct {
	ManagementToken string `json:"management_token"`
	ETag            string `json:"etag,omitempty"`
}

type transferResponse struct {
	transferSession
	ManagementToken string         `json:"management_token,omitempty"`
	Upload          *authorizedURL `json:"upload,omitempty"`
	Download        *authorizedURL `json:"download,omitempty"`
}

type authorizedURL struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
}

type objectBackend interface {
	PresignPut(ctx requestContext, objectID string, size int64, contentType, sha256 string, expires time.Time) (authorizedURL, error)
	PresignGet(ctx requestContext, objectID, filename string, expires time.Time) (authorizedURL, error)
	Head(ctx requestContext, objectID string) (objectInfo, error)
	Delete(ctx requestContext, objectID string) error
}

type objectInfo struct {
	Size   int64
	ETag   string
	SHA256 string
}

// requestContext is deliberately the small context interface used by storage
// adapters, which keeps fake adapters simple in tests.
type requestContext interface {
	Done() <-chan struct{}
	Err() error
	Value(any) any
	Deadline() (time.Time, bool)
}

func (s *controlServer) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/transfers" {
		http.NotFound(w, r)
		return
	}
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if s.control == nil {
		http.Error(w, "control store is not configured", http.StatusServiceUnavailable)
		return
	}
	var input createTransferRequest
	if err := decodeStrictJSON(w, r, maxControlBody, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	session, token, err := s.control.create(input.Mode, input.Filename, input.ContentType, input.ExpectedSize, input.SHA256)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	response := transferResponse{transferSession: session, ManagementToken: token}
	writeJSONStatus(w, http.StatusCreated, response)
}

func (s *controlServer) handleTransferAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/transfers/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || !validSessionID(parts[0]) {
		http.NotFound(w, r)
		return
	}
	code, action := parts[0], parts[1]
	switch action {
	case "upload-started":
		s.handleUploadStarted(w, r, code)
	case "upload-authorize":
		s.handleUploadAuthorize(w, r, code)
	case "upload-complete":
		s.handleUploadComplete(w, r, code)
	case "status":
		s.handleManagedStatus(w, r, code)
	case "revoke":
		s.handleRevoke(w, r, code)
	default:
		http.NotFound(w, r)
	}
}

func (s *controlServer) handleUploadStarted(w http.ResponseWriter, r *http.Request, code string) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var input managedRequest
	if err := decodeStrictJSON(w, r, maxControlBody, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	session, err := s.control.markUploadStarted(code, input.ManagementToken, s.offlineLifetime)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	if session.Mode == modeOnline {
		s.store.start(code, s.offlineLifetime)
	}
	writeJSON(w, transferResponse{transferSession: session})
}

func (s *controlServer) handleUploadAuthorize(w http.ResponseWriter, r *http.Request, code string) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var input managedRequest
	if err := decodeStrictJSON(w, r, maxControlBody, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	session, err := s.control.getManaged(code, input.ManagementToken)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	if session.State != stateCreated && session.State != stateUploading {
		http.Error(w, "upload authorization is unavailable", http.StatusConflict)
		return
	}
	expires := session.ExpiresAt
	if session.State == stateCreated {
		limit := s.control.currentTime().Add(createdLifetime)
		if limit.Before(expires) {
			expires = limit
		}
	}
	upload, err := s.authorizeUpload(r, session, expires)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, transferResponse{transferSession: session, Upload: &upload})
}

func (s *controlServer) authorizeUpload(r *http.Request, session transferSession, expires time.Time) (authorizedURL, error) {
	switch session.Mode {
	case modeServerA:
		if s.storageBaseURL == "" || len(s.storageSecret) == 0 {
			return authorizedURL{}, errors.New("storage server A is not configured")
		}
		claims := storageClaims{
			Version: 1, Method: http.MethodPut, ObjectID: session.StorageObjectID,
			ExpectedSize: session.ExpectedSize, ExpiresAt: expires.Unix(),
		}
		var err error
		claims.Nonce, err = randomHex(16)
		if err != nil {
			return authorizedURL{}, err
		}
		token, err := signStorageClaims(claims, s.storageSecret)
		if err != nil {
			return authorizedURL{}, err
		}
		target, err := joinPublicURL(s.storageBaseURL, "/upload/"+token)
		if err != nil {
			return authorizedURL{}, err
		}
		return authorizedURL{
			URL: target, Method: http.MethodPut,
			Headers: map[string]string{"Content-Type": session.ContentType},
		}, nil
	case modeR2:
		if s.r2 == nil {
			return authorizedURL{}, errors.New("R2 is not configured")
		}
		return s.r2.PresignPut(r.Context(), session.StorageObjectID, session.ExpectedSize,
			session.ContentType, session.SHA256Hex, expires)
	default:
		return authorizedURL{}, errors.New("online transfers do not use an upload URL")
	}
}

func (s *controlServer) handleUploadComplete(w http.ResponseWriter, r *http.Request, code string) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var input managedRequest
	if err := decodeStrictJSON(w, r, maxControlBody, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	session, err := s.control.getManaged(code, input.ManagementToken)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	if session.Mode == modeR2 {
		if s.r2 == nil {
			http.Error(w, "R2 is not configured", http.StatusServiceUnavailable)
			return
		}
		info, err := s.r2.Head(r.Context(), session.StorageObjectID)
		if err != nil {
			http.Error(w, "R2 object verification failed", http.StatusBadGateway)
			return
		}
		if info.Size != session.ExpectedSize || normalizeETag(info.ETag) != normalizeETag(input.ETag) {
			http.Error(w, "R2 object size or ETag does not match", http.StatusConflict)
			return
		}
	}
	if session.Mode == modeServerA {
		info, headErr := s.headStorageObject(r, session)
		if headErr != nil {
			http.Error(w, "server A object verification failed", http.StatusBadGateway)
			return
		}
		if info.Size != session.ExpectedSize || info.SHA256 != session.SHA256Hex ||
			normalizeETag(info.ETag) != normalizeETag(input.ETag) {
			http.Error(w, "server A object size, hash, or ETag does not match", http.StatusConflict)
			return
		}
	}
	session, err = s.control.markReady(code, input.ManagementToken, input.ETag)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, transferResponse{transferSession: session})
}

func (s *controlServer) headStorageObject(r *http.Request, session transferSession) (objectInfo, error) {
	claims := storageClaims{
		Version: 1, Method: http.MethodGet, ObjectID: session.StorageObjectID,
		ExpectedSize: session.ExpectedSize,
		ExpiresAt:    s.control.currentTime().Add(time.Minute).Unix(),
	}
	claims.Nonce, _ = randomHex(16)
	token, err := signStorageClaims(claims, s.storageSecret)
	if err != nil {
		return objectInfo{}, err
	}
	target, err := joinPublicURL(s.storageBaseURL, "/download/"+token)
	if err != nil {
		return objectInfo{}, err
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodHead, target, nil)
	if err != nil {
		return objectInfo{}, err
	}
	response, err := s.client().Do(request)
	if err != nil {
		return objectInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return objectInfo{}, fmt.Errorf("storage HEAD returned %s", response.Status)
	}
	size, err := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return objectInfo{}, err
	}
	return objectInfo{
		Size: size, ETag: response.Header.Get("ETag"),
		SHA256: response.Header.Get("X-Content-SHA256"),
	}, nil
}

func (s *controlServer) handleManagedStatus(w http.ResponseWriter, r *http.Request, code string) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var input managedRequest
	if err := decodeStrictJSON(w, r, maxControlBody, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	session, err := s.control.getManaged(code, input.ManagementToken)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, transferResponse{transferSession: session})
}

func (s *controlServer) handleRevoke(w http.ResponseWriter, r *http.Request, code string) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var input managedRequest
	if err := decodeStrictJSON(w, r, maxControlBody, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	before, err := s.control.getManaged(code, input.ManagementToken)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	session, err := s.control.revoke(code, input.ManagementToken)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	deleted := false
	if before.Mode == modeR2 && s.r2 != nil {
		deleted = s.r2.Delete(r.Context(), before.StorageObjectID) == nil
	}
	if before.Mode == modeServerA && s.storageBaseURL != "" && len(s.storageSecret) > 0 {
		deleted = s.deleteStorageObject(r.Context(), before) == nil
	}
	if deleted {
		_ = s.control.markStorageDeleted(before.ID)
	}
	writeJSON(w, transferResponse{transferSession: session})
}

func (s *controlServer) deleteStorageObject(ctx context.Context, session transferSession) error {
	claims := storageClaims{
		Version: 1, Method: http.MethodDelete, ObjectID: session.StorageObjectID,
		ExpectedSize: session.ExpectedSize,
		ExpiresAt:    s.control.currentTime().Add(time.Minute).Unix(),
	}
	claims.Nonce, _ = randomHex(16)
	token, err := signStorageClaims(claims, s.storageSecret)
	if err != nil {
		return err
	}
	target, err := joinPublicURL(s.storageBaseURL, "/objects/"+token)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	response, err := s.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("storage deletion returned %s", response.Status)
	}
	return nil
}

func (s *controlServer) runCleanup(ctx context.Context) {
	s.cleanupExpired(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupExpired(ctx)
		}
	}
}

func (s *controlServer) cleanupExpired(ctx context.Context) {
	sessions, err := s.control.pendingExpiredObjects()
	if err != nil {
		return
	}
	for _, session := range sessions {
		var deleteErr error
		switch session.Mode {
		case modeR2:
			if s.r2 == nil {
				continue
			}
			deleteErr = s.r2.Delete(ctx, session.StorageObjectID)
		case modeServerA:
			if s.storageBaseURL == "" || len(s.storageSecret) == 0 {
				continue
			}
			deleteErr = s.deleteStorageObject(ctx, session)
		}
		if deleteErr == nil {
			_ = s.control.markStorageDeleted(session.ID)
		}
	}
}

func (s *controlServer) handleReceive(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	code := strings.TrimPrefix(r.URL.Path, "/api/receive/")
	if !validSessionID(code) {
		http.NotFound(w, r)
		return
	}
	session, err := s.control.getReady(code)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	response := transferResponse{transferSession: session}
	switch session.Mode {
	case modeServerA:
		claims := storageClaims{
			Version: 1, Method: http.MethodGet, ObjectID: session.StorageObjectID,
			ExpectedSize: session.ExpectedSize, ExpiresAt: session.ExpiresAt.Unix(),
			Filename: session.Filename,
		}
		claims.Nonce, _ = randomHex(16)
		token, signErr := signStorageClaims(claims, s.storageSecret)
		if signErr != nil {
			http.Error(w, signErr.Error(), http.StatusServiceUnavailable)
			return
		}
		target, joinErr := joinPublicURL(s.storageBaseURL, "/download/"+token)
		if joinErr != nil {
			http.Error(w, joinErr.Error(), http.StatusServiceUnavailable)
			return
		}
		response.Download = &authorizedURL{URL: target, Method: http.MethodGet}
	case modeR2:
		if s.r2 == nil {
			http.Error(w, "R2 is not configured", http.StatusServiceUnavailable)
			return
		}
		download, signErr := s.r2.PresignGet(r.Context(), session.StorageObjectID, session.Filename, session.ExpiresAt)
		if signErr != nil {
			http.Error(w, signErr.Error(), http.StatusServiceUnavailable)
			return
		}
		response.Download = &download
	}
	writeJSON(w, response)
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, max int64, target any) error {
	if r.ContentLength > max {
		return &http.MaxBytesError{Limit: max}
	}
	body := http.MaxBytesReader(w, r.Body, max)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func requireSmallOrEmptyBody(w http.ResponseWriter, r *http.Request, max int64) error {
	if r.ContentLength > max {
		return &http.MaxBytesError{Limit: max}
	}
	if r.Body == nil {
		return nil
	}
	body := http.MaxBytesReader(w, r.Body, max)
	_, err := io.Copy(io.Discard, body)
	return err
}

func (s *controlServer) client() *http.Client {
	if s.httpClient != nil {
		return s.httpClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid JSON request", http.StatusBadRequest)
}

func writeSessionError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "transfer not found or unavailable", http.StatusNotFound)
		return
	}
	if strings.Contains(err.Error(), "cannot") {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	http.Error(w, "transfer request failed", http.StatusBadRequest)
}

func joinPublicURL(base, suffix string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid public storage URL")
	}
	parsed.Path = path.Join(parsed.Path, suffix)
	return parsed.String(), nil
}

func normalizeETag(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"`)
}

func sameOriginHost(a, b string) bool {
	left, errA := url.Parse(a)
	right, errB := url.Parse(b)
	return errA == nil && errB == nil && strings.EqualFold(left.Hostname(), right.Hostname())
}

func ensureDataPlaneIsNotControlPlane(controlURL, dataURL string) error {
	if controlURL != "" && sameOriginHost(controlURL, dataURL) {
		return fmt.Errorf("data plane URL must not use control server C host")
	}
	return nil
}
