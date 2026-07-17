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
	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestWebImportUsesBooksRouteAndPRG(t *testing.T) {
	handler, service, _ := newHTTPTestHandler(t)
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
	handler, service, _ := newHTTPTestHandler(t)
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
	handler, _, _ := newHTTPTestHandler(t)
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, multipartRequest(t, "/books/import", "book.txt", strings.Repeat("x", int(library.MaxTXTBytes+1))))
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "upload_too_large") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestWebGenerateCreatesTaskAndLegacyConvertRouteIsGone(t *testing.T) {
	handler, service, taskService := newHTTPTestHandler(t)
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n正文")})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"format": {"epub"}, "title": {"任务标题"}}
	request := httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/generate", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "queued") {
		t.Fatalf("generate response = %d, %q", response.Code, response.Header().Get("Location"))
	}
	tasks, err := taskService.List(context.Background(), result.Book.ID)
	if err != nil || len(tasks) != 1 || tasks[0].Status != task.Queued || tasks[0].Type != task.GenerateEPUB {
		t.Fatalf("tasks = %#v, %v", tasks, err)
	}
	legacy := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(legacy, httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader(form.Encode())))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy convert status = %d", legacy.Code)
	}
}

func newHTTPTestHandler(t *testing.T) (Handler, *library.Service, *task.Service) {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := library.New(storage)
	t.Cleanup(func() { _ = service.Close() })
	taskService := task.NewService(storage)
	generationService := generation.NewService(service, taskService, txtconfig.Defaults())
	return Handler{Library: service, Generation: generationService}, service, taskService
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
