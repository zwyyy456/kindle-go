package transfer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeObjectBackend struct {
	put     authorizedURL
	get     authorizedURL
	info    objectInfo
	deleted string
}

type handlerRoundTripper struct{ handler http.Handler }

func (h handlerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func (f *fakeObjectBackend) PresignPut(_ requestContext, objectID string, _ int64, _, _ string, _ time.Time) (authorizedURL, error) {
	f.put = authorizedURL{URL: "https://r2.example/" + objectID, Method: http.MethodPut}
	return f.put, nil
}
func (f *fakeObjectBackend) PresignGet(_ requestContext, objectID, _ string, _ time.Time) (authorizedURL, error) {
	f.get = authorizedURL{URL: "https://r2.example/" + objectID, Method: http.MethodGet}
	return f.get, nil
}
func (f *fakeObjectBackend) Head(_ requestContext, _ string) (objectInfo, error) {
	return f.info, nil
}
func (f *fakeObjectBackend) Delete(_ requestContext, objectID string) error {
	f.deleted = objectID
	return nil
}

func newControlHandler(t *testing.T, configure func(*controlServer)) (http.Handler, *sessionStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := openSessionStore(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := &controlServer{control: store, offlineLifetime: 2 * time.Hour}
	if configure != nil {
		configure(server)
	}
	return server.routes(), store, dir
}

func createTransferViaAPI(t *testing.T, handler http.Handler, mode transferMode, payload []byte) transferResponse {
	t.Helper()
	body, _ := json.Marshal(createTransferRequest{
		Mode: mode, Filename: "book.azw3", ContentType: "application/octet-stream",
		ExpectedSize: int64(len(payload)), SHA256: hashHex(payload),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/transfers", bytes.NewReader(body)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create = %d %q", recorder.Code, recorder.Body.String())
	}
	var response transferResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func postManaged(t *testing.T, handler http.Handler, target, token, etag string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(managedRequest{ManagementToken: token, ETag: etag})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body)))
	return recorder
}

func TestControlR2FlowUsesDirectDataPlaneAndVerifiesHead(t *testing.T) {
	payload := []byte("r2 payload")
	fake := &fakeObjectBackend{}
	handler, _, _ := newControlHandler(t, func(server *controlServer) { server.r2 = fake })
	created := createTransferViaAPI(t, handler, modeR2, payload)
	if got := postManaged(t, handler, "/api/transfers/"+created.Code+"/upload-started", created.ManagementToken, ""); got.Code != http.StatusOK {
		t.Fatalf("start = %d %q", got.Code, got.Body.String())
	}
	auth := postManaged(t, handler, "/api/transfers/"+created.Code+"/upload-authorize", created.ManagementToken, "")
	if auth.Code != http.StatusOK {
		t.Fatalf("authorize = %d %q", auth.Code, auth.Body.String())
	}
	var authorized transferResponse
	if err := json.Unmarshal(auth.Body.Bytes(), &authorized); err != nil {
		t.Fatal(err)
	}
	if authorized.Upload == nil || !strings.HasPrefix(authorized.Upload.URL, "https://r2.example/") {
		t.Fatalf("upload = %#v", authorized.Upload)
	}
	fake.info = objectInfo{Size: int64(len(payload)), ETag: `"etag-1"`, SHA256: hashHex(payload)}
	complete := postManaged(t, handler, "/api/transfers/"+created.Code+"/upload-complete", created.ManagementToken, "etag-1")
	if complete.Code != http.StatusOK {
		t.Fatalf("complete = %d %q", complete.Code, complete.Body.String())
	}
	receive := httptest.NewRecorder()
	handler.ServeHTTP(receive, httptest.NewRequest(http.MethodGet, "/api/receive/"+created.Code, nil))
	if receive.Code != http.StatusOK || !strings.Contains(receive.Body.String(), "https://r2.example/") {
		t.Fatalf("receive = %d %q", receive.Code, receive.Body.String())
	}
	revoke := postManaged(t, handler, "/api/transfers/"+created.Code+"/revoke", created.ManagementToken, "")
	if revoke.Code != http.StatusOK || fake.deleted == "" {
		t.Fatalf("revoke = %d deleted=%q", revoke.Code, fake.deleted)
	}
}

func TestControlRejectsLargeBodiesAndHasNoUploadRoute(t *testing.T) {
	handler, _, dir := newControlHandler(t, nil)
	for _, target := range []string{"/api/transfers", "/api/session", "/api/signal", "/api/capabilities"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(strings.Repeat("x", maxControlBody+1)))
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s status = %d, body = %q", target, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("file body")))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unexpected C upload route status = %d", recorder.Code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if bytes.Contains(data, []byte("file body")) {
			t.Fatalf("control plane persisted file body in %s", entry.Name())
		}
	}
}

func TestControlServerAEndToEnd(t *testing.T) {
	payload := []byte("direct server A body")
	origin := "https://transfer.example.com"
	storage, storageHandler, _ := newTestStorage(t)
	storage.AllowedOrigin = origin
	storageClient := &http.Client{Transport: handlerRoundTripper{handler: storageHandler}}
	handler, controlStore, _ := newControlHandler(t, func(server *controlServer) {
		server.publicURL = origin
		server.storageBaseURL = "https://storage.test"
		server.storageSecret = storage.Secret
		server.httpClient = storageClient
	})
	controlStore.now = storage.Now
	created := createTransferViaAPI(t, handler, modeServerA, payload)
	if got := postManaged(t, handler, "/api/transfers/"+created.Code+"/upload-started", created.ManagementToken, ""); got.Code != http.StatusOK {
		t.Fatal(got.Body.String())
	}
	authResult := postManaged(t, handler, "/api/transfers/"+created.Code+"/upload-authorize", created.ManagementToken, "")
	var auth transferResponse
	if err := json.Unmarshal(authResult.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	uploadURL, err := url.Parse(auth.Upload.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, uploadURL.RequestURI(), bytes.NewReader(payload))
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/octet-stream")
	upload := httptest.NewRecorder()
	storageHandler.ServeHTTP(upload, request)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload = %d %q", upload.Code, upload.Body.String())
	}
	etag := upload.Header().Get("ETag")
	complete := postManaged(t, handler, "/api/transfers/"+created.Code+"/upload-complete", created.ManagementToken, etag)
	if complete.Code != http.StatusOK {
		t.Fatalf("complete = %d %q", complete.Code, complete.Body.String())
	}
	receive := httptest.NewRecorder()
	handler.ServeHTTP(receive, httptest.NewRequest(http.MethodGet, "/api/receive/"+created.Code, nil))
	if receive.Code != http.StatusOK {
		t.Fatalf("receive = %d %q", receive.Code, receive.Body.String())
	}
	var resolved transferResponse
	if err := json.Unmarshal(receive.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	downloadURL, err := url.Parse(resolved.Download.URL)
	if err != nil {
		t.Fatal(err)
	}
	download := httptest.NewRecorder()
	storageHandler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, downloadURL.RequestURI(), nil))
	got, _ := io.ReadAll(download.Body)
	if download.Code != http.StatusOK || !bytes.Equal(got, payload) {
		t.Fatalf("download = %d %q", download.Code, got)
	}
}

func TestDataPlaneCannotShareControlHost(t *testing.T) {
	if err := ensureDataPlaneIsNotControlPlane("https://transfer.example.com", "https://transfer.example.com/storage"); err == nil {
		t.Fatal("same control/data host accepted")
	}
	if err := ensureDataPlaneIsNotControlPlane("https://transfer.example.com", "https://storage.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := ensureDataPlaneIsNotControlPlane("https://transfer.example.com:443", "https://transfer.example.com:9443"); err == nil {
		t.Fatal("same hostname on another port accepted")
	}
}
