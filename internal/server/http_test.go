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
	detail := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, location, nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "Generate") {
		t.Fatalf("detail = %d, %q", detail.Code, detail.Body.String())
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
	status := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/tasks/"+tasks[0].ID+"/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"status":"queued"`) {
		t.Fatalf("task status = %d, %q", status.Code, status.Body.String())
	}
	canceled := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(canceled, httptest.NewRequest(http.MethodPost, "/tasks/"+tasks[0].ID+"/cancel", nil))
	if canceled.Code != http.StatusSeeOther {
		t.Fatalf("cancel status = %d", canceled.Code)
	}
	retried := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(retried, httptest.NewRequest(http.MethodPost, "/tasks/"+tasks[0].ID+"/retry", nil))
	if retried.Code != http.StatusSeeOther {
		t.Fatalf("retry status = %d", retried.Code)
	}
	tasks, err = taskService.List(context.Background(), result.Book.ID)
	if err != nil || len(tasks) != 2 || tasks[0].RetryOfTaskID != tasks[1].ID || tasks[0].Status != task.Queued || tasks[1].Status != task.Canceled {
		t.Fatalf("tasks after retry = %#v, %v", tasks, err)
	}
	legacy := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(legacy, httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader(form.Encode())))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy convert status = %d", legacy.Code)
	}
}

func TestDownloadsUseFileIDAndKindleMuxRemainsReadOnly(t *testing.T) {
	handler, service, _ := newHTTPTestHandler(t)
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("download body")})
	if err != nil {
		t.Fatal(err)
	}
	download := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/files/"+result.Book.Original.ID+"/download", nil))
	if download.Code != http.StatusOK || download.Body.String() != "download body" || !strings.Contains(download.Header().Get("Content-Disposition"), "book.txt") {
		t.Fatalf("download = %d, %q, %q", download.Code, download.Body.String(), download.Header().Get("Content-Disposition"))
	}
	legacy := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/download/"+result.Book.ID+"/original", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy download status = %d", legacy.Code)
	}
	kindleMutation := httptest.NewRecorder()
	handler.KindleMux().ServeHTTP(kindleMutation, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	if kindleMutation.Code != http.StatusNotFound {
		t.Fatalf("Kindle task route status = %d", kindleMutation.Code)
	}
}

func TestBookDeletionRequiresConfirmationAndRemovesBook(t *testing.T) {
	handler, service, _ := newHTTPTestHandler(t)
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("source")})
	if err != nil {
		t.Fatal(err)
	}
	missingConfirmation := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(missingConfirmation, httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/delete", nil))
	if missingConfirmation.Code != http.StatusBadRequest {
		t.Fatalf("missing confirmation status = %d", missingConfirmation.Code)
	}
	form := url.Values{"confirm": {"delete"}}
	request := httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/delete", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleted := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(deleted, request)
	if deleted.Code != http.StatusSeeOther || !strings.Contains(deleted.Header().Get("Location"), "deleted") {
		t.Fatalf("delete response = %d, %q", deleted.Code, deleted.Header().Get("Location"))
	}
	if _, ok, err := service.GetBook(context.Background(), result.Book.ID); err != nil || ok {
		t.Fatalf("deleted book = %v, %v", ok, err)
	}
}

func TestTXTPreviewUsesSubmittedParameters(t *testing.T) {
	handler, service, _ := newHTTPTestHandler(t)
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章 开始\n\n正文。")})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"title": {"预览标题"}, "split_level": {"1"}}
	request := httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/txt-preview", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "utf-8") || !strings.Contains(response.Body.String(), "第一章 开始") {
		t.Fatalf("preview = %d, %q", response.Code, response.Body.String())
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
	return Handler{Library: service, Generation: generationService, Tasks: taskService}, service, taskService
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
