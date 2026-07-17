package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/store"
)

func TestWebImportUsesBooksRouteAndPRG(t *testing.T) {
	handler, service := newHTTPTestHandler(t)
	request := multipartRequest(t, "/books/import", "book.txt", "正文")
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	location := response.Header().Get("Location")
	if !strings.HasPrefix(location, "/books/") {
		t.Fatalf("location = %q", location)
	}
	books, err := service.AllBooks(context.Background())
	if err != nil || len(books) != 1 {
		t.Fatalf("books = %d, %v", len(books), err)
	}

	old := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(old, httptest.NewRequest(http.MethodPost, "/upload", nil))
	if old.Code != http.StatusNotFound {
		t.Fatalf("legacy upload status = %d", old.Code)
	}
}

func TestWebDuplicateConfirmationIsOneTime(t *testing.T) {
	handler, service := newHTTPTestHandler(t)
	for index := 0; index < 2; index++ {
		response := httptest.NewRecorder()
		handler.WebMux().ServeHTTP(response, multipartRequest(t, "/books/import", "book.txt", "same"))
		if response.Code != http.StatusSeeOther {
			t.Fatalf("import %d status = %d", index, response.Code)
		}
		if index == 0 {
			continue
		}
		location, err := url.Parse(response.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		token := location.Query().Get("duplicate")
		if token == "" {
			t.Fatalf("duplicate location = %q", location)
		}
		form := url.Values{"action": {"import"}}
		confirm := httptest.NewRequest(http.MethodPost, "/books/import/"+token+"/confirm", strings.NewReader(form.Encode()))
		confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		confirmed := httptest.NewRecorder()
		handler.WebMux().ServeHTTP(confirmed, confirm)
		if confirmed.Code != http.StatusSeeOther {
			t.Fatalf("confirm status = %d", confirmed.Code)
		}
		replayRequest := httptest.NewRequest(http.MethodPost, "/books/import/"+token+"/confirm", strings.NewReader(form.Encode()))
		replayRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		replayed := httptest.NewRecorder()
		handler.WebMux().ServeHTTP(replayed, replayRequest)
		if replayed.Code != http.StatusSeeOther || !strings.Contains(replayed.Header().Get("Location"), "invalid") {
			t.Fatalf("replay location = %q", replayed.Header().Get("Location"))
		}
	}
	books, err := service.AllBooks(context.Background())
	if err != nil || len(books) != 2 {
		t.Fatalf("books = %d, %v", len(books), err)
	}
}

func TestWebImportMapsHardLimitToRequestEntityTooLarge(t *testing.T) {
	handler, _ := newHTTPTestHandler(t)
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, multipartRequest(t, "/books/import", "book.txt", strings.Repeat("x", int(library.MaxTXTBytes+1))))
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "upload_too_large") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func newHTTPTestHandler(t *testing.T) (Handler, *library.Service) {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := library.New(storage)
	t.Cleanup(func() { _ = service.Close() })
	return Handler{Library: service, BaseConfig: txtconfig.Defaults()}, service
}

func multipartRequest(t *testing.T, target, name, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
