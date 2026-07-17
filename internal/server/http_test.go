package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestWebImportUsesBooksRouteAndPRG(t *testing.T) {
	handler, service, _, _ := newHTTPTestHandler(t)
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
	handler, service, _, _ := newHTTPTestHandler(t)
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
	handler, _, _, _ := newHTTPTestHandler(t)
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, multipartRequest(t, "/books/import", "book.txt", strings.Repeat("x", int(library.MaxTXTBytes+1))))
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "upload_too_large") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestWebGenerateCreatesTaskAndLegacyConvertRouteIsGone(t *testing.T) {
	handler, service, taskService, _ := newHTTPTestHandler(t)
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

func TestWebStartsSingleActiveProofreadTaskWithSettingsSnapshot(t *testing.T) {
	handler, service, taskService, _ := newHTTPTestHandler(t)
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n正文")})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/proofreads", nil)
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "queued") {
		t.Fatalf("proofread response = %d, %q", response.Code, response.Header().Get("Location"))
	}
	tasks, err := taskService.List(context.Background(), result.Book.ID)
	if err != nil || len(tasks) != 1 || tasks[0].Type != task.Proofread || tasks[0].Status != task.Queued || !strings.Contains(tasks[0].ParametersJSON, `"batch_size":12000`) {
		t.Fatalf("proofread task = %#v, %v", tasks, err)
	}
	duplicate := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/proofreads", nil))
	if duplicate.Code != http.StatusSeeOther || !strings.Contains(duplicate.Header().Get("Location"), "already+has+an+active") {
		t.Fatalf("duplicate proofread = %d, %q", duplicate.Code, duplicate.Header().Get("Location"))
	}
}

func TestWebReviewsCandidatesAndAppendsDecision(t *testing.T) {
	handler, service, _, storage := newHTTPTestHandler(t)
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n错字\n")})
	if err != nil {
		t.Fatal(err)
	}
	created, err := handler.Proofreads.Create(context.Background(), result.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{string(task.Proofread)}, time.Now()); err != nil || !ok {
		t.Fatalf("claim proofread = %v, %v", ok, err)
	}
	workRel := filepath.ToSlash(filepath.Join("work", created.ID, "proofread-state"))
	workPath, err := storage.ResolveRel(workRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workPath, "candidates.jsonl"), []byte(`{"candidate_id":"candidate-web","context":"1 第一章\n2 错字"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runID := "run-web"
	location := `{"line":2,"start_char":4,"end_char":6}`
	err = storage.CommitProofreadRun(context.Background(), store.ProofreadRunRecord{
		ID: runID, BookID: result.Book.ID, SourceFileID: result.Book.Original.ID, TaskID: created.ID, SourceSHA256: result.Book.Original.SHA256,
		Format: "txt", BatchSize: 12000, Concurrency: 3, EngineVersion: "test", CreatedAt: time.Now(),
	}, []store.ProofreadCandidateRecord{{
		ID: "candidate-web", Kind: "text", LocationJSON: location, ExpectedOriginal: "错字", Category: "wrong_character",
		FirstConfidence: "review", FirstReplacement: "正字", Verification: "review", VerifiedReplacement: "正字", Reason: "上下文待确认",
	}}, workRel, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	detail := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/books/"+result.Book.ID, nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "/proofreads/"+runID) {
		t.Fatalf("book detail = %d, %q", detail.Code, detail.Body.String())
	}
	reviewURL := "/books/" + result.Book.ID + "/proofreads/" + runID
	review := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(review, httptest.NewRequest(http.MethodGet, reviewURL, nil))
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), "candidate-web") || !strings.Contains(review.Body.String(), "1 第一章") {
		t.Fatalf("review = %d, %q", review.Code, review.Body.String())
	}
	form := url.Values{"decision": {"modify"}, "replacement": {"正字"}}
	request := httptest.NewRequest(http.MethodPost, "/candidates/candidate-web/decision", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), reviewURL) {
		t.Fatalf("decision = %d, %q", response.Code, response.Header().Get("Location"))
	}
	decisions, err := storage.CandidateDecisions(context.Background(), runID)
	if err != nil || len(decisions) != 1 || decisions[0].Decision != "modify" || decisions[0].Replacement != "正字" {
		t.Fatalf("decisions = %#v, %v", decisions, err)
	}
}

func TestDownloadsUseFileIDAndKindleMuxRemainsReadOnly(t *testing.T) {
	handler, service, _, _ := newHTTPTestHandler(t)
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
	handler, service, _, _ := newHTTPTestHandler(t)
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
	handler, service, _, _ := newHTTPTestHandler(t)
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

func TestIncompatibleEPUBShowsStructuredReportAndBlocksGeneration(t *testing.T) {
	handler, service, _, _ := newHTTPTestHandler(t)
	data := epubArchiveForHTTP(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="missing.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
	})
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "bad.epub", Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/books/"+result.Book.ID, nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "EPUB compatibility: failed") || !strings.Contains(page.Body.String(), "package_resource_missing") || strings.Contains(page.Body.String(), "Queue generation") {
		t.Fatalf("EPUB detail = %d, %q", page.Code, page.Body.String())
	}
	form := url.Values{"format": {"azw3"}}
	request := httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/generate", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "epub_incompatible") {
		t.Fatalf("blocked generation = %d, %q", response.Code, response.Header().Get("Location"))
	}
}

func TestSettingsPageAndKindleEPUBToggleTakeEffectImmediately(t *testing.T) {
	handler, service, _, _ := newHTTPTestHandler(t)
	data := epubArchiveForHTTP(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`,
	})
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.epub", Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	before := httptest.NewRecorder()
	handler.KindleMux().ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/?all=1", nil))
	if strings.Contains(before.Body.String(), result.Book.DisplayName) {
		t.Fatalf("EPUB shown before toggle: %q", before.Body.String())
	}
	values, err := handler.Settings.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settingsForm := url.Values{
		"author": {values.Author}, "language": {values.Language}, "cover": {"1"},
		"h1_regex": {values.TXT.H1Regex}, "h2_regex": {values.TXT.H2Regex}, "split_level": {"2"},
		"merge_lines": {"1"}, "trim_blank_lines": {"1"}, "replace_json": {"[]"},
		"line_height": {"1.8"}, "paragraph_indent": {values.Style.ParagraphIndent},
		"paragraph_spacing": {values.Style.ParagraphSpacing}, "text_align": {values.Style.TextAlign},
		"kindle_show_epub": {"1"}, "proofread_batch_size": {"12000"}, "proofread_concurrency": {"3"},
	}
	settingsRequest := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(settingsForm.Encode()))
	settingsRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settingsResponse := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(settingsResponse, settingsRequest)
	if settingsResponse.Code != http.StatusSeeOther || !strings.Contains(settingsResponse.Header().Get("Location"), "saved") {
		t.Fatalf("settings save = %d, %q", settingsResponse.Code, settingsResponse.Header().Get("Location"))
	}
	after := httptest.NewRecorder()
	handler.KindleMux().ServeHTTP(after, httptest.NewRequest(http.MethodGet, "/?all=1", nil))
	if after.Code != http.StatusOK || !strings.Contains(after.Body.String(), result.Book.DisplayName) || !strings.Contains(after.Body.String(), "EPUB") {
		t.Fatalf("EPUB after toggle = %d, %q", after.Code, after.Body.String())
	}
	page := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Global settings") || !strings.Contains(page.Body.String(), "Codex CLI") {
		t.Fatalf("settings page = %d, %q", page.Code, page.Body.String())
	}
}

func TestBookFormDoesNotPersistPerBookConversionSettings(t *testing.T) {
	handler, service, taskService, _ := newHTTPTestHandler(t)
	values, err := handler.Settings.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	values.Style.LineHeight = 1.9
	if err := handler.Settings.Save(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	result, err := service.Import(context.Background(), library.ImportRequest{Filename: "book.txt", Reader: strings.NewReader("第一章\n正文")})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"format": {"epub"}, "line_height": {"2.4"}}
	request := httptest.NewRequest(http.MethodPost, "/books/"+result.Book.ID+"/generate", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("generate status = %d", response.Code)
	}
	tasks, err := taskService.List(context.Background(), result.Book.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("task snapshot = %#v, %v", tasks, err)
	}
	var params generation.Parameters
	if err := json.Unmarshal([]byte(tasks[0].ParametersJSON), &params); err != nil || params.Style.LineHeight != 2.4 {
		t.Fatalf("task parameters = %#v, %v", params, err)
	}
	detail := httptest.NewRecorder()
	handler.WebMux().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/books/"+result.Book.ID, nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `name="line_height" value="1.9"`) || strings.Contains(detail.Body.String(), `name="line_height" value="2.4"`) {
		t.Fatalf("book defaults = %d, %q", detail.Code, detail.Body.String())
	}
}

func epubArchiveForHTTP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func newHTTPTestHandler(t *testing.T) (Handler, *library.Service, *task.Service, *store.Store) {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := library.New(storage)
	t.Cleanup(func() { _ = service.Close() })
	taskService := task.NewService(storage)
	settingsService := appsettings.New(storage, txtconfig.Defaults(), appsettings.Runtime{LibraryDir: service.Root(), LibrarySource: "test"})
	if err := settingsService.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	generationService := generation.NewService(service, taskService, txtconfig.Defaults(), settingsService)
	proofreadService := proofread.NewService(storage, service, taskService, settingsService)
	return Handler{
		Library: service, Generation: generationService, Tasks: taskService, Settings: settingsService, Proofreads: proofreadService,
		Diagnostics: func(context.Context) proofread.DependencyDiagnostics {
			return proofread.DependencyDiagnostics{PythonPath: "/python3", PythonVersion: "Python test", CodexPath: "/codex", CodexVersion: "codex test"}
		},
	}, service, taskService, storage
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
