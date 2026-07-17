package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

const recentWindow = 24 * time.Hour

type Handler struct {
	Library     *library.Service
	Generation  *generation.Service
	Tasks       *task.Service
	Settings    *appsettings.Service
	Diagnostics func(context.Context) proofread.DependencyDiagnostics
	CheckCodex  func(context.Context) (string, error)
	Proofreads  *proofread.Service
	Logger      *log.Logger
}

func (h Handler) WebMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleWebRoot)
	mux.HandleFunc("/books", h.handleBooks)
	mux.HandleFunc("/books/", h.handleBookRoute)
	mux.HandleFunc("/candidates/", h.handleCandidateRoute)
	mux.HandleFunc("/proofreads/", h.handleProofreadRoute)
	mux.HandleFunc("/files/", h.handleFileRoute)
	mux.HandleFunc("/tasks", h.handleTasks)
	mux.HandleFunc("/tasks/", h.handleTaskRoute)
	mux.HandleFunc("/settings", h.handleSettings)
	mux.HandleFunc("/settings/check-codex", h.handleCheckCodex)
	return mux
}

func (h Handler) KindleMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleKindleIndex)
	mux.HandleFunc("/files/", h.handleKindleFileRoute)
	return mux
}

func (h Handler) handleWebIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, err := h.Library.ListBooks(r.Context(), library.BookQuery{
		Search: r.URL.Query().Get("q"), Sort: r.URL.Query().Get("sort"), StatusFilter: r.URL.Query().Get("status"), Page: parseInt(r.URL.Query().Get("page")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := webPageData{
		Records: h.recordViews(page.Books),
		Message: r.URL.Query().Get("message"),
		Query:   r.URL.Query().Get("q"), Sort: r.URL.Query().Get("sort"), StatusFilter: r.URL.Query().Get("status"),
		Page: page.Page, PreviousPage: page.Page - 1, NextPage: page.Page + 1,
		HasPrevious: page.HasPrevious, HasNext: page.HasNext, Total: page.Total,
	}
	if token := r.URL.Query().Get("duplicate"); token != "" {
		if pending, ok := h.Library.PendingDuplicate(token); ok {
			data.Duplicate = duplicateView{Token: token, Filename: pending.Filename, Existing: h.recordViews(pending.Existing)}
		} else {
			data.Message = "Duplicate confirmation expired; import the file again."
		}
	}
	if err := webTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleWebRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/books", http.StatusSeeOther)
}

func (h Handler) handleBooks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleWebIndex(w, r)
	case http.MethodPost:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h Handler) handleBookRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/books/")
	if path == "import" {
		h.handleImport(w, r)
		return
	}
	if strings.HasPrefix(path, "import/") && strings.HasSuffix(path, "/confirm") {
		token := strings.TrimSuffix(strings.TrimPrefix(path, "import/"), "/confirm")
		h.handleImportConfirm(w, r, token)
		return
	}
	parts := strings.Split(path, "/")
	if r.Method == http.MethodGet && len(parts) == 3 && parts[0] != "" && parts[1] == "proofreads" && parts[2] != "" {
		h.handleProofreadReview(w, r, parts[0], parts[2])
		return
	}
	if r.Method == http.MethodGet && path != "" && !strings.Contains(path, "/") {
		h.handleBookDetail(w, r, path)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/generate") {
		bookID := strings.TrimSuffix(path, "/generate")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleGenerate(w, r, bookID)
			return
		}
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/txt-preview") {
		bookID := strings.TrimSuffix(path, "/txt-preview")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleTXTPreview(w, r, bookID)
			return
		}
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/proofreads") {
		bookID := strings.TrimSuffix(path, "/proofreads")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleCreateProofread(w, r, bookID)
			return
		}
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/delete") {
		bookID := strings.TrimSuffix(path, "/delete")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleDeleteBook(w, r, bookID)
			return
		}
	}
	http.NotFound(w, r)
}

func (h Handler) handleCandidateRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/candidates/")
	if r.Method != http.MethodPost || !strings.HasSuffix(path, "/decision") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(path, "/decision")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if h.Proofreads == nil {
		http.Error(w, "proofreading service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	run, err := h.Proofreads.Decide(r.Context(), id, proofread.DecisionRequest{Decision: r.Form.Get("decision"), Replacement: r.Form.Get("replacement")})
	if run.ID == "" {
		http.NotFound(w, r)
		return
	}
	message := "candidate decision saved"
	if err != nil {
		message = "decision failed: " + err.Error()
	}
	http.Redirect(w, r, "/books/"+run.BookID+"/proofreads/"+run.ID+"?message="+urlMessage(message), http.StatusSeeOther)
}

func (h Handler) handleProofreadRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/proofreads/")
	if r.Method != http.MethodPost || !strings.HasSuffix(path, "/revisions") {
		http.NotFound(w, r)
		return
	}
	runID := strings.TrimSuffix(path, "/revisions")
	if runID == "" || strings.Contains(runID, "/") {
		http.NotFound(w, r)
		return
	}
	if h.Proofreads == nil {
		http.Error(w, "proofreading service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	created, err := h.Proofreads.CreateRevision(r.Context(), runID, r.Form.Get("confirm_unresolved") == "1")
	if err != nil {
		run, ok, runErr := h.Proofreads.Run(r.Context(), runID)
		if runErr == nil && ok {
			http.Redirect(w, r, "/books/"+run.BookID+"/proofreads/"+runID+"?message="+urlMessage("revision failed: "+err.Error()), http.StatusSeeOther)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/books/"+created.BookID+"?message=revision+task+queued", http.StatusSeeOther)
}

func (h Handler) handleKindleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	showEPUB := false
	if h.Settings != nil {
		values, err := h.Settings.Current(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		showEPUB = values.KindleShowEPUB
	}
	books, err := h.Library.LatestKindleFiles(r.Context(), showEPUB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var records []recordView
	cutoff := time.Now().Add(-recentWindow)
	for _, value := range books {
		if r.URL.Query().Get("all") != "1" && value.Book.ImportedAt.Before(cutoff) {
			continue
		}
		records = append(records, recordView{
			ID: value.Book.ID, OriginalName: value.Book.DisplayName,
			UploadedAt: value.Book.ImportedAt.Format("2006-01-02 15:04"),
			Files:      fileViews(value.Files),
		})
	}
	data := kindlePageData{
		Records: records,
		ShowAll: r.URL.Query().Get("all") == "1",
	}
	if err := kindleTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, library.MaxEPUBBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "upload_too_large: request exceeds the 64 MiB EPUB limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing upload file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	result, err := h.Library.Import(r.Context(), library.ImportRequest{Filename: header.Filename, Reader: file, Now: time.Now()})
	if err != nil {
		code := library.ErrorCode(err)
		status := http.StatusInternalServerError
		if code != "" {
			status = http.StatusBadRequest
		}
		if code == "upload_too_large" {
			status = http.StatusRequestEntityTooLarge
		}
		message := err.Error()
		if code != "" {
			message = code + ": " + message
		}
		http.Error(w, message, status)
		return
	}
	if result.Duplicate {
		http.Redirect(w, r, "/books?duplicate="+url.QueryEscape(result.DuplicateToken), http.StatusSeeOther)
		return
	}
	h.logFile("import", result.Book.Original)
	http.Redirect(w, r, "/books/"+result.Book.ID+"?message=imported", http.StatusSeeOther)
}

func (h Handler) handleImportConfirm(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	book, err := h.Library.ConfirmImport(r.Context(), token, r.Form.Get("action"))
	if err != nil {
		http.Redirect(w, r, "/books?message="+urlMessage(err.Error()), http.StatusSeeOther)
		return
	}
	if book.ID == "" {
		http.Redirect(w, r, "/books?message=import+canceled", http.StatusSeeOther)
		return
	}
	if r.Form.Get("action") == "import" {
		h.logFile("import", book.Original)
	}
	http.Redirect(w, r, "/books/"+book.ID, http.StatusSeeOther)
}

func (h Handler) handleBookDetail(w http.ResponseWriter, r *http.Request, id string) {
	detail, ok, err := h.Library.GetBookDetail(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	var tasks []task.Task
	var runs []storeProofreadRunView
	if h.Tasks != nil {
		tasks, err = h.Tasks.List(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if h.Proofreads != nil {
		values, err := h.Proofreads.Runs(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, value := range values {
			runs = append(runs, storeProofreadRunView{ID: value.ID, Format: strings.ToUpper(value.Format), Model: value.Model, CompletedAt: value.CompletedAt.Format("2006-01-02 15:04")})
		}
	}
	files := make([]fileView, 0, len(detail.Files))
	var generationInputs []generationInputView
	for _, file := range detail.Files {
		view := newFileView(file)
		if file.Role != "original" && file.Role != "revision" {
			files = append(files, view)
			continue
		}
		compatible := file.Format == "txt"
		if file.Format == "epub" {
			report, found, reportErr := h.Library.CompatibilityForFile(r.Context(), file.ID)
			if reportErr != nil {
				http.Error(w, reportErr.Error(), http.StatusInternalServerError)
				return
			}
			compatible = found && report.Status == "passed"
			if found {
				view.CompatibilityStatus = report.Status
			}
		}
		files = append(files, view)
		if compatible {
			generationInputs = append(generationInputs, generationInputView{ID: file.ID, Name: file.DisplayName, Role: file.Role, HasUnresolved: file.HasUnresolved})
		}
	}
	data := bookPageData{
		Book: recordView{
			ID: detail.Book.ID, OriginalName: detail.Book.DisplayName,
			UploadedAt:   detail.Book.ImportedAt.Format("2006-01-02 15:04"),
			InputFormat:  detail.Book.SourceFormat,
			TitleDefault: strings.TrimSuffix(detail.Book.Original.DisplayName, filepath.Ext(detail.Book.Original.DisplayName)),
		},
		Files: files, Tasks: taskViews(tasks), ProofreadRuns: runs, GenerationInputs: generationInputs, Message: r.URL.Query().Get("message"), Compatibility: detail.Compatibility,
		CanGenerate: len(generationInputs) != 0,
	}
	if h.Settings != nil {
		defaults, err := h.Settings.Current(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data.Defaults = defaults
	}
	if err := bookTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleProofreadReview(w http.ResponseWriter, r *http.Request, bookID, runID string) {
	if h.Proofreads == nil {
		http.Error(w, "proofreading service is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, ok, err := h.Proofreads.Review(r.Context(), bookID, runID, proofread.ReviewQuery{Filter: r.URL.Query().Get("filter"), Page: parseInt(r.URL.Query().Get("page"))})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := proofreadPageData{
		BookID: bookID, Run: storeProofreadRunView{ID: page.Run.ID, Format: strings.ToUpper(page.Run.Format), Model: page.Run.Model, CompletedAt: page.Run.CompletedAt.Format("2006-01-02 15:04")},
		Candidates: page.Candidates, Filter: page.Filter, Page: page.Page, PreviousPage: page.Page - 1, NextPage: page.Page + 1,
		HasPrevious: page.HasPrevious, HasNext: page.HasNext, Total: page.Total, Unresolved: page.Unresolved, Message: r.URL.Query().Get("message"),
	}
	if err := proofreadTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleGenerate(w http.ResponseWriter, r *http.Request, bookID string) {
	if h.Generation == nil {
		http.Error(w, "generation service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	formats := r.Form["format"]
	if len(formats) == 0 {
		formats = []string{"azw3"}
	}
	_, err := h.Generation.Create(r.Context(), generation.CreateRequest{
		BookID: bookID, InputFileID: r.Form.Get("input_file_id"), Formats: formats,
		Options: generationOptionsFromForm(r),
	})
	if err != nil {
		http.Redirect(w, r, "/books/"+bookID+"?message="+urlMessage("generate failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books/"+bookID+"?message=task+queued", http.StatusSeeOther)
}

func (h Handler) handleTXTPreview(w http.ResponseWriter, r *http.Request, bookID string) {
	if h.Generation == nil {
		http.Error(w, "generation service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	analysis, err := h.Generation.PreviewTXT(r.Context(), bookID, generationOptionsFromForm(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data := txtPreviewPageData{
		BookID: bookID, Charset: analysis.Charset,
		OriginalLines: analysis.Stats.Text.OriginalLines, DroppedLines: analysis.Stats.Text.DroppedLines,
		BlankLines: analysis.Stats.Text.BlankLines, MergedLines: analysis.Stats.Text.MergedLines,
		Sections: analysis.Stats.SectionCount, Paragraphs: analysis.Stats.ParagraphCount,
	}
	for _, entry := range analysis.TOC {
		data.TOC = append(data.TOC, entry.Title)
		for _, child := range entry.Children {
			data.TOC = append(data.TOC, "— "+child.Title)
		}
	}
	if err := txtPreviewTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleCreateProofread(w http.ResponseWriter, r *http.Request, bookID string) {
	if h.Proofreads == nil {
		http.Error(w, "proofreading service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err := h.Proofreads.Create(r.Context(), bookID); err != nil {
		http.Redirect(w, r, "/books/"+bookID+"?message="+urlMessage("proofread failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books/"+bookID+"?message=proofread+task+queued", http.StatusSeeOther)
}

func generationOptionsFromForm(r *http.Request) generation.Options {
	return generation.Options{
		Title: r.Form.Get("title"), Author: r.Form.Get("author"), Language: r.Form.Get("language"),
		H1Regex: r.Form.Get("h1_regex"), H2Regex: r.Form.Get("h2_regex"),
		SplitLevel: parseInt(r.Form.Get("split_level")), LineHeight: parseFloat(r.Form.Get("line_height")),
		ParagraphSpacing: r.Form.Get("paragraph_spacing"), ParagraphIndent: r.Form.Get("paragraph_indent"), TextAlign: r.Form.Get("text_align"),
		Cover: formBool(r, "cover"), MergeLines: formBool(r, "merge_lines"), TrimBlankLines: formBool(r, "trim_blank_lines"),
	}
}

func formBool(r *http.Request, name string) *bool {
	if r.Form.Get(name+"_present") == "" {
		return nil
	}
	value := r.Form.Get(name) != ""
	return &value
}

func (h Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		http.Error(w, "settings service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		values, err := settingsFromForm(r)
		if err == nil {
			err = h.Settings.Save(r.Context(), values)
		}
		if err != nil {
			http.Redirect(w, r, "/settings?message="+urlMessage(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings?message=saved", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values, err := h.Settings.Current(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	diagnostics := proofread.DiagnoseDependencies(r.Context(), nil, "", "")
	if h.Diagnostics != nil {
		diagnostics = h.Diagnostics(r.Context())
	}
	data := settingsPageData{
		Values: values, Runtime: h.Settings.Runtime(), Diagnostics: diagnostics,
		Message: r.URL.Query().Get("message"), DropRegex: strings.Join(values.TXT.DropRegex, "\n"),
	}
	if h.Library != nil {
		data.System, err = h.Library.Diagnostics(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data.SystemFree = humanSize(int64(data.System.FreeBytes))
	}
	replacements, _ := json.Marshal(values.TXT.Replace)
	data.ReplaceJSON = string(replacements)
	if err := settingsTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleCheckCodex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	check := h.CheckCodex
	if check == nil {
		check = func(ctx context.Context) (string, error) { return proofread.CheckCodexLogin(ctx, nil, "") }
	}
	status, err := check(r.Context())
	message := "Codex check passed: " + status
	if err != nil {
		message = "Codex check failed: " + err.Error()
	}
	http.Redirect(w, r, "/settings?message="+urlMessage(message), http.StatusSeeOther)
}

func settingsFromForm(r *http.Request) (appsettings.Values, error) {
	values := appsettings.Values{
		Author: r.Form.Get("author"), Language: r.Form.Get("language"), Cover: r.Form.Get("cover") != "",
		KindleShowEPUB: r.Form.Get("kindle_show_epub") != "",
		TXT: txtconfig.TXTConfig{
			H1Regex: r.Form.Get("h1_regex"), H2Regex: r.Form.Get("h2_regex"), SplitLevel: parseInt(r.Form.Get("split_level")),
			MergeLines: r.Form.Get("merge_lines") != "", TrimBlankLines: r.Form.Get("trim_blank_lines") != "",
			DropRegex: nonBlankLines(r.Form.Get("drop_regex")),
		},
		Style: txtconfig.Style{
			LineHeight: parseFloat(r.Form.Get("line_height")), ParagraphIndent: r.Form.Get("paragraph_indent"),
			ParagraphSpacing: r.Form.Get("paragraph_spacing"), TextAlign: r.Form.Get("text_align"),
		},
		Proofread: appsettings.Proofread{Model: r.Form.Get("proofread_model"), BatchSize: parseInt(r.Form.Get("proofread_batch_size")), Concurrency: parseInt(r.Form.Get("proofread_concurrency"))},
	}
	if raw := strings.TrimSpace(r.Form.Get("replace_json")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &values.TXT.Replace); err != nil {
			return appsettings.Values{}, fmt.Errorf("replacement rules must be valid JSON: %w", err)
		}
	}
	return values, nil
}

func nonBlankLines(raw string) []string {
	var values []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values
}

func (h Handler) handleDeleteBook(w http.ResponseWriter, r *http.Request, bookID string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Form.Get("confirm") != "delete" {
		http.Error(w, "deletion confirmation is required", http.StatusBadRequest)
		return
	}
	if err := h.Library.DeleteBook(r.Context(), bookID); err != nil {
		http.Redirect(w, r, "/books/"+bookID+"?message="+urlMessage(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books?message=book+deleted", http.StatusSeeOther)
}

func parseInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func parseFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value
}

func (h Handler) handleFileRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/files/")
	if strings.HasSuffix(rest, "/delete") && r.Method == http.MethodPost {
		fileID := strings.TrimSuffix(rest, "/delete")
		if fileID == "" || strings.Contains(fileID, "/") {
			http.NotFound(w, r)
			return
		}
		_, file, err := h.Library.DownloadFile(r.Context(), fileID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := h.Library.DeleteFile(r.Context(), fileID); err != nil {
			http.Redirect(w, r, "/books/"+file.BookID+"?message="+urlMessage(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/books/"+file.BookID+"?message=file+deleted", http.StatusSeeOther)
		return
	}
	h.handleDownload(w, r)
}

func (h Handler) handleKindleFileRoute(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/download") {
		http.NotFound(w, r)
		return
	}
	h.handleDownload(w, r)
}

func (h Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/files/")
	if !strings.HasSuffix(rest, "/download") {
		http.NotFound(w, r)
		return
	}
	fileID := strings.TrimSuffix(rest, "/download")
	if fileID == "" || strings.Contains(fileID, "/") {
		http.NotFound(w, r)
		return
	}
	path, file, err := h.Library.DownloadFile(r.Context(), fileID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.DisplayName))
	h.logFile("download", file)
	http.ServeFile(w, r, path)
}

func (h Handler) logFile(event string, file library.File) {
	if h.Logger == nil {
		return
	}
	h.Logger.Printf("file event=%s file_id=%s book_id=%s role=%s format=%s size_bytes=%d", event, file.ID, file.BookID, file.Role, file.Format, file.Size)
}

func (h Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || h.Tasks == nil {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values, err := h.Tasks.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tasksTemplate.Execute(w, tasksPageData{Tasks: taskViews(values), Message: r.URL.Query().Get("message")}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleTaskRoute(w http.ResponseWriter, r *http.Request) {
	if h.Tasks == nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/tasks/")
	if r.Method == http.MethodGet && strings.HasSuffix(rest, ".json") && !strings.Contains(strings.TrimSuffix(rest, ".json"), "/") {
		h.writeTaskStatus(w, r, strings.TrimSuffix(rest, ".json"))
		return
	}
	if r.Method == http.MethodGet && rest != "" && !strings.Contains(rest, "/") {
		value, ok, err := h.Tasks.Get(r.Context(), rest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		events, err := h.Tasks.Events(r.Context(), rest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := taskDetailTemplate.Execute(w, taskDetailPageData{Task: taskViews([]task.Task{value})[0], Events: taskEventViews(events)}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, action := parts[0], parts[1]
	if action == "status" && r.Method == http.MethodGet {
		h.writeTaskStatus(w, r, id)
		return
	}
	if r.Method != http.MethodPost || (action != "cancel" && action != "retry") {
		http.NotFound(w, r)
		return
	}
	original, ok, err := h.Tasks.Get(r.Context(), id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	message := "task canceled"
	if action == "cancel" {
		err = h.Tasks.Cancel(r.Context(), id)
	} else {
		_, err = h.Tasks.Retry(r.Context(), id)
		message = "task retry queued"
	}
	if err != nil {
		message = err.Error()
	}
	http.Redirect(w, r, "/books/"+original.BookID+"?message="+urlMessage(message), http.StatusSeeOther)
}

func (h Handler) writeTaskStatus(w http.ResponseWriter, r *http.Request, id string) {
	value, ok, err := h.Tasks.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	events, err := h.Tasks.Events(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": value.ID, "status": value.Status, "stage": value.Stage,
		"current": value.ProgressCurrent, "total": value.ProgressTotal,
		"error_code": value.ErrorCode, "error_message": value.ErrorMessage,
		"events": events,
	})
}

func (h Handler) recordViews(records []library.Book) []recordView {
	views := make([]recordView, 0, len(records))
	for _, record := range records {
		view := recordView{
			ID:              record.ID,
			OriginalName:    record.DisplayName,
			UploadedAt:      record.ImportedAt.Format("2006-01-02 15:04"),
			LastError:       record.LegacyLastError,
			ProofreadStatus: record.ProofreadStatus,
		}
		view.Files = displayFileViews(record)
		view.InputFormat = strings.ToLower(record.Original.Format)
		view.TitleDefault = strings.TrimSuffix(record.Original.DisplayName, filepath.Ext(record.Original.DisplayName))
		if view.TitleDefault == "" {
			view.TitleDefault = strings.TrimSuffix(record.DisplayName, filepath.Ext(record.DisplayName))
		}
		views = append(views, view)
	}
	return views
}

func displayFileViews(record library.Book) []fileView {
	original := record.Original
	output := record.LatestArtifact
	var files []fileView
	if original.ID != "" {
		files = append(files, newFileView(original))
	}
	if output.ID != "" {
		files = append(files, newFileView(output))
	}
	return files
}

func newFileView(file library.File) fileView {
	return fileView{
		ID:         file.ID,
		Kind:       file.Role,
		Name:       file.DisplayName,
		Format:     strings.ToUpper(file.Format),
		Size:       humanSize(file.Size),
		URL:        downloadURL(file.ID),
		CreatedAt:  file.CreatedAt.Format("2006-01-02 15:04"),
		CanDelete:  file.Role != "original",
		Unresolved: file.HasUnresolved,
	}
}

func fileViews(files []library.File) []fileView {
	views := make([]fileView, 0, len(files))
	for _, file := range files {
		views = append(views, newFileView(file))
	}
	return views
}

func downloadURL(fileID string) string {
	return "/files/" + fileID + "/download"
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func urlMessage(message string) string {
	return url.QueryEscape(message)
}

type webPageData struct {
	Records      []recordView
	Message      string
	Duplicate    duplicateView
	Query        string
	Sort         string
	StatusFilter string
	Page         int
	PreviousPage int
	NextPage     int
	HasPrevious  bool
	HasNext      bool
	Total        int
}

type duplicateView struct {
	Token    string
	Filename string
	Existing []recordView
}

type kindlePageData struct {
	Records []recordView
	ShowAll bool
}

type bookPageData struct {
	Book             recordView
	Files            []fileView
	Tasks            []taskView
	ProofreadRuns    []storeProofreadRunView
	GenerationInputs []generationInputView
	Message          string
	Compatibility    *library.CompatibilityReport
	CanGenerate      bool
	Defaults         appsettings.Values
}

type generationInputView struct {
	ID            string
	Name          string
	Role          string
	HasUnresolved bool
}

type storeProofreadRunView struct {
	ID          string
	Format      string
	Model       string
	CompletedAt string
}

type proofreadPageData struct {
	BookID       string
	Run          storeProofreadRunView
	Candidates   []proofread.CandidateView
	Filter       string
	Page         int
	PreviousPage int
	NextPage     int
	HasPrevious  bool
	HasNext      bool
	Total        int
	Unresolved   int
	Message      string
}

type settingsPageData struct {
	Values      appsettings.Values
	Runtime     appsettings.Runtime
	Diagnostics proofread.DependencyDiagnostics
	Message     string
	DropRegex   string
	ReplaceJSON string
	System      store.Diagnostics
	SystemFree  string
}

type tasksPageData struct {
	Tasks   []taskView
	Message string
}

type taskDetailPageData struct {
	Task   taskView
	Events []taskEventView
}

type txtPreviewPageData struct {
	BookID        string
	Charset       string
	OriginalLines int
	DroppedLines  int
	BlankLines    int
	MergedLines   int
	Sections      int
	Paragraphs    int
	TOC           []string
}

type recordView struct {
	ID              string
	OriginalName    string
	UploadedAt      string
	LastError       string
	Files           []fileView
	InputFormat     string
	TitleDefault    string
	ProofreadStatus string
}

type fileView struct {
	ID                  string
	Kind                string
	Name                string
	Format              string
	Size                string
	URL                 string
	CreatedAt           string
	CanDelete           bool
	Unresolved          bool
	CompatibilityStatus string
}

type taskView struct {
	ID        string
	BookID    string
	Type      string
	Status    string
	Stage     string
	Progress  string
	Error     string
	CanCancel bool
	CanRetry  bool
	Active    bool
	CreatedAt string
}

type taskEventView struct {
	Seq       int
	Level     string
	Stage     string
	Message   string
	CreatedAt string
}

func taskViews(values []task.Task) []taskView {
	views := make([]taskView, 0, len(values))
	for _, value := range values {
		progress := ""
		if value.ProgressTotal > 0 {
			progress = fmt.Sprintf("%d/%d", value.ProgressCurrent, value.ProgressTotal)
		}
		views = append(views, taskView{
			ID: value.ID, BookID: value.BookID, Type: string(value.Type), Status: string(value.Status),
			Stage: value.Stage, Progress: progress, Error: value.ErrorMessage,
			CanCancel: value.Status == task.Queued || value.Status == task.Running,
			CanRetry:  value.Status == task.Failed || value.Status == task.Canceled,
			Active:    value.Status == task.Queued || value.Status == task.Running,
			CreatedAt: value.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return views
}

func taskEventViews(values []task.Event) []taskEventView {
	views := make([]taskEventView, 0, len(values))
	for _, value := range values {
		views = append(views, taskEventView{
			Seq: value.Seq, Level: value.Level, Stage: value.Stage, Message: value.Message,
			CreatedAt: value.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return views
}

var webTemplate = template.Must(template.New("web").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Kindle Go</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
    h1 { margin-bottom: 8px; }
    fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 16px 0; }
    label { display: block; margin: 8px 0; }
    input[type=text], input[type=number], select { min-width: 320px; max-width: 100%; padding: 6px; }
    button { padding: 7px 12px; }
    .record { border-top: 1px solid #d8dee6; padding: 18px 0; }
    .files a { margin-right: 8px; }
    .error { color: #9b1c1c; }
    .muted { color: #627282; }
  </style>
</head>
<body>
  <h1>Kindle Go</h1>
  <p><a href="/tasks">Tasks</a> · <a href="/settings">Settings</a></p>
  <p class="muted">Upload files from this computer, convert when needed, then download from Kindle.</p>
  {{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}

  <fieldset>
    <legend>Upload</legend>
    <form method="post" action="/books/import" enctype="multipart/form-data">
      <input type="file" name="file" accept=".txt,.epub" required>
      <button type="submit">Import</button>
    </form>
    <p class="muted">TXT up to 32 MiB; EPUB up to 64 MiB and 512 MiB expanded.</p>
  </fieldset>

  {{if .Duplicate.Token}}
  <fieldset>
    <legend>Duplicate source</legend>
    <p><strong>{{.Duplicate.Filename}}</strong> has the same content as an existing book.</p>
    {{range .Duplicate.Existing}}<p>{{.OriginalName}} — {{.UploadedAt}}</p>{{end}}
    <form method="post" action="/books/import/{{.Duplicate.Token}}/confirm">
      <button name="action" value="open" type="submit">Open existing book</button>
      <button name="action" value="import" type="submit">Import as a new book</button>
      <button name="action" value="cancel" type="submit">Cancel</button>
    </form>
  </fieldset>
  {{end}}

  <form method="get" action="/books">
    <input type="text" name="q" value="{{.Query}}" placeholder="Search book names">
    <select name="status"><option value="">All proofread statuses</option><option value="not_started" {{if eq .StatusFilter "not_started"}}selected{{end}}>Not started</option><option value="queued" {{if eq .StatusFilter "queued"}}selected{{end}}>Queued</option><option value="running" {{if eq .StatusFilter "running"}}selected{{end}}>Running</option><option value="completed" {{if eq .StatusFilter "completed"}}selected{{end}}>Completed</option><option value="failed" {{if eq .StatusFilter "failed"}}selected{{end}}>Failed</option><option value="canceled" {{if eq .StatusFilter "canceled"}}selected{{end}}>Canceled</option></select>
    <select name="sort"><option value="">Newest first</option><option value="name_asc" {{if eq .Sort "name_asc"}}selected{{end}}>Name A–Z</option><option value="imported_asc" {{if eq .Sort "imported_asc"}}selected{{end}}>Oldest first</option><option value="status_asc" {{if eq .Sort "status_asc"}}selected{{end}}>Proofread status</option></select>
    <button type="submit">Apply</button>
  </form>
  <p class="muted">{{.Total}} book(s), page {{.Page}}</p>

  {{range .Records}}
  <section class="record">
    <h2><a href="/books/{{.ID}}">{{.OriginalName}}</a></h2>
    <p class="muted">Uploaded {{.UploadedAt}} · proofread: {{.ProofreadStatus}}</p>
    {{if .LastError}}<p class="error">{{.LastError}}</p>{{end}}
    <div class="files">
      {{range .Files}}
        <p><a href="{{.URL}}">{{.Name}}</a> <span class="muted">{{.Kind}}, {{.Format}}, {{.Size}}</span></p>
      {{end}}
    </div>
    <p><a href="/books/{{.ID}}">Open details, generate files, and view tasks</a></p>
  </section>
  {{else}}
  <p>No uploads yet.</p>
  {{end}}
  <p>{{if .HasPrevious}}<a href="/books?q={{urlquery .Query}}&status={{.StatusFilter}}&sort={{.Sort}}&page={{.PreviousPage}}">Previous</a>{{end}} {{if .HasNext}}<a href="/books?q={{urlquery .Query}}&status={{.StatusFilter}}&sort={{.Sort}}&page={{.NextPage}}">Next</a>{{end}}</p>
</body>
</html>`))

var bookTemplate = template.Must(template.New("book").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>{{.Book.OriginalName}} — Kindle Go</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
    fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 18px 0; }
    label { display: block; margin: 8px 0; }
    input[type=text], input[type=number] { min-width: 320px; padding: 6px; }
    table { width: 100%; border-collapse: collapse; margin: 12px 0; }
    th, td { text-align: left; border-bottom: 1px solid #d8dee6; padding: 8px; }
    .error { color: #9b1c1c; }
    .muted { color: #627282; }
    form.inline { display: inline; }
  </style>
</head>
<body>
  <p><a href="/books">← Library</a> · <a href="/tasks">All tasks</a></p>
  <h1>{{.Book.OriginalName}}</h1>
  <p class="muted">Imported {{.Book.UploadedAt}} · {{.Book.InputFormat}}</p>
  {{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}

  {{if .CanGenerate}}<fieldset>
    <legend>Generate</legend>
    <form method="post" action="/books/{{.Book.ID}}/generate">
      <label>Input file <select name="input_file_id">{{range .GenerationInputs}}<option value="{{.ID}}">{{.Name}} ({{.Role}}{{if .HasUnresolved}}, unresolved locations kept{{end}})</option>{{end}}</select></label>
      <label><input type="checkbox" name="format" value="azw3" checked> AZW3</label>
      {{if eq .Book.InputFormat "txt"}}<label><input type="checkbox" name="format" value="epub"> EPUB</label>{{end}}
      <label>Title <input type="text" name="title" value="{{.Book.TitleDefault}}"></label>
      <label>Author <input type="text" name="author" value="{{.Defaults.Author}}"></label>
      <label>Language <input type="text" name="language" value="{{.Defaults.Language}}"></label>
      <input type="hidden" name="cover_present" value="1"><label><input type="checkbox" name="cover" value="1" {{if .Defaults.Cover}}checked{{end}}> Generate text cover</label>
      {{if eq .Book.InputFormat "txt"}}
      <label>H1 regex <input type="text" name="h1_regex" value="{{.Defaults.TXT.H1Regex}}"></label>
      <label>H2 regex <input type="text" name="h2_regex" value="{{.Defaults.TXT.H2Regex}}"></label>
      <label>Split level <input type="number" name="split_level" min="1" max="2" value="{{.Defaults.TXT.SplitLevel}}"></label>
      <input type="hidden" name="merge_lines_present" value="1"><label><input type="checkbox" name="merge_lines" value="1" {{if .Defaults.TXT.MergeLines}}checked{{end}}> Merge wrapped lines</label>
      <input type="hidden" name="trim_blank_lines_present" value="1"><label><input type="checkbox" name="trim_blank_lines" value="1" {{if .Defaults.TXT.TrimBlankLines}}checked{{end}}> Compress consecutive blank lines</label>
      <label>Line height <input type="text" name="line_height" value="{{.Defaults.Style.LineHeight}}"></label>
      <label>Paragraph spacing <input type="text" name="paragraph_spacing" value="{{.Defaults.Style.ParagraphSpacing}}"></label>
      <label>Paragraph indent <input type="text" name="paragraph_indent" value="{{.Defaults.Style.ParagraphIndent}}"></label>
      <label>Text align <input type="text" name="text_align" value="{{.Defaults.Style.TextAlign}}"></label>
      {{end}}
      {{if eq .Book.InputFormat "txt"}}<button type="submit" formaction="/books/{{.Book.ID}}/txt-preview">Preview TXT</button>{{end}}
      <button type="submit">Queue generation</button>
    </form>
  </fieldset>{{else}}<p class="error">AZW3 generation is unavailable because this EPUB did not pass compatibility analysis.</p>{{end}}

  <form method="post" action="/books/{{.Book.ID}}/proofreads"><button type="submit">Start AI proofreading with Codex CLI</button></form>

  {{if .ProofreadRuns}}<h2>Proofreading runs</h2>
  <table><thead><tr><th>Completed</th><th>Format</th><th>Model</th><th>Candidates</th></tr></thead><tbody>
  {{range .ProofreadRuns}}<tr><td>{{.CompletedAt}}</td><td>{{.Format}}</td><td>{{if .Model}}{{.Model}}{{else}}Codex default{{end}}</td><td><a href="/books/{{$.Book.ID}}/proofreads/{{.ID}}">Review candidates</a></td></tr>{{end}}
  </tbody></table>{{end}}

  {{with .Compatibility}}
  <h2>EPUB compatibility: {{.Status}}</h2>
  <p>Title: {{.Metadata.Title}} · Author: {{.Metadata.Author}} · Language: {{.Metadata.Language}}</p>
  {{if .Cover.ImageHref}}<p>Cover: {{.Cover.ImageHref}}</p>{{end}}
  <h3>Spine</h3><ul>{{range .Spine}}<li>{{.Href}} — {{.MediaType}} {{if .Title}}— {{.Title}}{{end}}</li>{{else}}<li>No readable spine items.</li>{{end}}</ul>
  <h3>Table of contents</h3><ul>{{range .TOC}}<li>{{.Title}} — {{.Href}}</li>{{else}}<li>No table of contents.</li>{{end}}</ul>
  <h3>Resources</h3><ul>{{range .Resources}}<li>{{.Href}} — {{.MediaType}} — {{.Size}} bytes {{if not .Exists}}(missing){{end}}</li>{{end}}</ul>
  {{if .Issues}}<h3>Blocking issues</h3><ul>{{range .Issues}}<li class="error"><strong>{{.Code}}</strong> [{{.Stage}}] {{.Message}}{{if .Document}} — document: {{.Document}}{{end}}{{if .Resource}} — resource: {{.Resource}}{{end}}</li>{{end}}</ul>{{end}}
  {{end}}

  <h2>Files</h2>
  <table><thead><tr><th>Name</th><th>Role</th><th>Format</th><th>Size</th><th>Created</th></tr></thead><tbody>
  {{range .Files}}<tr><td><a href="{{.URL}}">{{.Name}}</a>{{if .Unresolved}} <span class="error">(contains unresolved source locations)</span>{{end}}{{if .CompatibilityStatus}} <span class="muted">(EPUB compatibility: {{.CompatibilityStatus}})</span>{{end}}{{if .CanDelete}} <form class="inline" method="post" action="/files/{{.ID}}/delete"><button type="submit">Delete</button></form>{{end}}</td><td>{{.Kind}}</td><td>{{.Format}}</td><td>{{.Size}}</td><td>{{.CreatedAt}}</td></tr>{{else}}<tr><td colspan="5">No files.</td></tr>{{end}}
  </tbody></table>

  <h2>Tasks</h2>
  <table><thead><tr><th>Type</th><th>Status</th><th>Stage</th><th>Progress</th><th>Created</th><th>Actions</th></tr></thead><tbody>
  {{range .Tasks}}<tr data-task-id="{{.ID}}" data-task-status="{{.Status}}"><td><a href="/tasks/{{.ID}}">{{.Type}}</a></td><td>{{.Status}}{{if .Error}}<div class="error">{{.Error}}</div>{{end}}</td><td>{{.Stage}}</td><td>{{.Progress}}</td><td>{{.CreatedAt}}</td><td>
    {{if .CanCancel}}<form class="inline" method="post" action="/tasks/{{.ID}}/cancel"><button type="submit">Cancel</button></form>{{end}}
    {{if .CanRetry}}<form class="inline" method="post" action="/tasks/{{.ID}}/retry"><button type="submit">Retry from beginning</button></form>{{end}}
  </td></tr>{{else}}<tr><td colspan="6">No tasks.</td></tr>{{end}}
  </tbody></table>
  <details>
    <summary>Delete this book</summary>
    <p class="error">This permanently deletes the imported copy, all revisions, artifacts, reports, and task history. The file you originally imported from outside this library is not affected.</p>
    <form method="post" action="/books/{{.Book.ID}}/delete"><label><input type="checkbox" name="confirm" value="delete" required> I understand this cannot be undone.</label><button type="submit">Delete book</button></form>
  </details>
  <script>
    const active = [...document.querySelectorAll('[data-task-id]')].filter(row => ['queued','running'].includes(row.dataset.taskStatus));
    if (active.length) setTimeout(async () => {
      const changed = await Promise.all(active.map(async row => {
        const result = await fetch('/tasks/' + row.dataset.taskId + '.json');
        if (!result.ok) return false;
        const task = await result.json();
        return task.status !== row.dataset.taskStatus;
      }));
      if (changed.some(Boolean)) location.reload(); else location.reload();
    }, 2000);
  </script>
</body>
</html>`))

var tasksTemplate = template.Must(template.New("tasks").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Tasks — Kindle Go</title></head><body>
<p><a href="/books">← Library</a></p><h1>Tasks</h1>{{if .Message}}<p>{{.Message}}</p>{{end}}
<table><thead><tr><th>Book</th><th>Type</th><th>Status</th><th>Stage</th><th>Progress</th><th>Created</th></tr></thead><tbody>
{{range .Tasks}}<tr><td><a href="/books/{{.BookID}}">{{.BookID}}</a></td><td><a href="/tasks/{{.ID}}">{{.Type}}</a></td><td>{{.Status}}{{if .Error}} — {{.Error}}{{end}}</td><td>{{.Stage}}</td><td>{{.Progress}}</td><td>{{.CreatedAt}}</td></tr>{{else}}<tr><td colspan="6">No tasks.</td></tr>{{end}}
</tbody></table></body></html>`))

var taskDetailTemplate = template.Must(template.New("task-detail").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Task {{.Task.ID}} — Kindle Go</title><style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
table { width: 100%; border-collapse: collapse; } th, td { text-align: left; border-bottom: 1px solid #d8dee6; padding: 8px; }
.error { color: #9b1c1c; } .warning { color: #8a5b00; } .muted { color: #627282; }
</style></head><body>
<p><a href="/tasks">← All tasks</a> · <a href="/books/{{.Task.BookID}}">Book</a></p>
<h1>{{.Task.Type}}</h1><p>Status: <strong>{{.Task.Status}}</strong> · stage: {{.Task.Stage}} · progress: {{.Task.Progress}}</p>
{{if .Task.Error}}<p class="error">{{.Task.Error}}</p>{{end}}
<h2>Event timeline</h2><table><thead><tr><th>Time</th><th>Level</th><th>Stage</th><th>Event</th></tr></thead><tbody>
{{range .Events}}<tr><td>{{.CreatedAt}}</td><td class="{{.Level}}">{{.Level}}</td><td>{{.Stage}}</td><td>{{.Message}}</td></tr>{{else}}<tr><td colspan="4">No events.</td></tr>{{end}}
</tbody></table></body></html>`))

var proofreadTemplate = template.Must(template.New("proofread").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Proofreading candidates — Kindle Go</title><style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
.candidate { border: 1px solid #ccd3dc; border-radius: 6px; padding: 16px; margin: 18px 0; }
.muted { color: #627282; } .error { color: #9b1c1c; } .automatic { color: #176b3a; }
pre { white-space: pre-wrap; background: #f4f6f8; padding: 12px; overflow-wrap: anywhere; }
form.inline { display: inline; } input[type=text] { min-width: 280px; padding: 5px; }
</style></head><body>
<p><a href="/books/{{.BookID}}">← Back to book</a> · <a href="/tasks">All tasks</a></p>
<h1>Proofreading candidates</h1>
<p class="muted">Run {{.Run.ID}} · {{.Run.Format}} · completed {{.Run.CompletedAt}} · model: {{if .Run.Model}}{{.Run.Model}}{{else}}Codex default{{end}}</p>
{{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}
<form method="get"><label>Filter <select name="filter">
<option value="">All</option><option value="pending" {{if eq .Filter "pending"}}selected{{end}}>Pending</option>
<option value="automatic" {{if eq .Filter "automatic"}}selected{{end}}>Automatic</option>
<option value="accepted" {{if eq .Filter "accepted"}}selected{{end}}>Accepted</option>
<option value="modified" {{if eq .Filter "modified"}}selected{{end}}>Modified</option>
<option value="rejected" {{if eq .Filter "rejected"}}selected{{end}}>Rejected</option>
<option value="conflict" {{if eq .Filter "conflict"}}selected{{end}}>Conflicts</option>
</select></label><button type="submit">Apply</button></form>
<p class="muted">{{.Total}} candidate(s), page {{.Page}}</p>
<form method="post" action="/proofreads/{{.Run.ID}}/revisions">
{{if .Unresolved}}<p><strong>{{.Unresolved}} unresolved candidate(s)</strong> will keep their exact source text.</p><label><input type="checkbox" name="confirm_unresolved" value="1" required> Generate anyway and keep every unresolved location unchanged.</label>{{end}}
<button type="submit">Queue immutable revised file, report, and audit</button></form>
{{range .Candidates}}<section class="candidate" id="{{.Candidate.ID}}">
<h2>{{.Candidate.ExpectedOriginal}} → {{.Candidate.FirstReplacement}}</h2>
<p><strong>Status:</strong> <span class="{{if eq .Outcome "automatic"}}automatic{{end}}">{{.Outcome}}</span> · <strong>Category:</strong> {{.Candidate.Category}} · <strong>Location:</strong> {{.Location}}</p>
{{if .ConflictIDs}}<p class="error"><strong>Conflict:</strong> overlaps applied candidate(s) {{range .ConflictIDs}}<a href="#{{.}}">{{.}}</a> {{end}}. Reject one before applying the other.</p>{{end}}
<p>First review: <strong>{{.Candidate.FirstConfidence}}</strong>, proposed <code>{{.Candidate.FirstReplacement}}</code> — {{.Candidate.Reason}}</p>
<p>Isolated verification: <strong>{{.Candidate.Verification}}</strong>, proposed <code>{{.Candidate.VerifiedReplacement}}</code></p>
<pre>{{.Context}}</pre>
<form class="inline" method="post" action="/candidates/{{.Candidate.ID}}/decision"><input type="hidden" name="decision" value="accept"><button type="submit">Accept first proposal</button></form>
<form class="inline" method="post" action="/candidates/{{.Candidate.ID}}/decision"><input type="hidden" name="decision" value="reject"><button type="submit">Reject / keep original</button></form>
{{if eq .Candidate.Kind "text"}}<form method="post" action="/candidates/{{.Candidate.ID}}/decision"><input type="hidden" name="decision" value="modify"><label>Modified replacement <input type="text" name="replacement" value="{{.Candidate.FirstReplacement}}" required></label><button type="submit">Save modified replacement</button></form>{{end}}
{{if .History}}<details><summary>Decision history ({{len .History}})</summary><ol>{{range .History}}<li>{{.CreatedAt}} — {{.Decision}}{{if .Replacement}}: <code>{{.Replacement}}</code>{{end}}</li>{{end}}</ol></details>{{end}}
</section>{{else}}<p>No candidates match this filter.</p>{{end}}
<p>{{if .HasPrevious}}<a href="?filter={{urlquery .Filter}}&page={{.PreviousPage}}">Previous</a>{{end}} {{if .HasNext}}<a href="?filter={{urlquery .Filter}}&page={{.NextPage}}">Next</a>{{end}}</p>
</body></html>`))

var settingsTemplate = template.Must(template.New("settings").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Settings — Kindle Go</title><style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 18px 0; }
label { display: block; margin: 8px 0; } input[type=text], input[type=number], textarea { min-width: 420px; max-width: 100%; padding: 6px; }
.muted { color: #627282; }
</style></head><body>
<p><a href="/books">← Library</a></p><h1>Global settings</h1>{{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}
<fieldset><legend>Runtime (startup only)</legend>
<p>Library: {{.Runtime.LibraryDir}} <span class="muted">({{.Runtime.LibrarySource}})</span></p>
<p>Web UI: {{.Runtime.WebAddr}} <span class="muted">({{.Runtime.WebAddrSource}})</span></p>
<p>Kindle: {{.Runtime.KindleAddr}} <span class="muted">({{.Runtime.KindleSource}})</span></p>
{{if .Runtime.ConfigPath}}<p>Config: {{.Runtime.ConfigPath}}</p>{{end}}
</fieldset>
<form method="post" action="/settings">
<fieldset><legend>TXT conversion defaults</legend>
<label>Default author <input type="text" name="author" value="{{.Values.Author}}"></label>
<label>Default language <input type="text" name="language" value="{{.Values.Language}}" required></label>
<label><input type="checkbox" name="cover" value="1" {{if .Values.Cover}}checked{{end}}> Generate text cover</label>
<label>H1 regex <input type="text" name="h1_regex" value="{{.Values.TXT.H1Regex}}" required></label>
<label>H2 regex <input type="text" name="h2_regex" value="{{.Values.TXT.H2Regex}}" required></label>
<label>Split level <input type="number" name="split_level" min="1" max="2" value="{{.Values.TXT.SplitLevel}}" required></label>
<label><input type="checkbox" name="merge_lines" value="1" {{if .Values.TXT.MergeLines}}checked{{end}}> Merge wrapped lines</label>
<label><input type="checkbox" name="trim_blank_lines" value="1" {{if .Values.TXT.TrimBlankLines}}checked{{end}}> Compress consecutive blank lines</label>
<label>Drop regexes (one per line)<textarea name="drop_regex" rows="4">{{.DropRegex}}</textarea></label>
<label>Replacement rules (JSON array)<textarea name="replace_json" rows="4">{{.ReplaceJSON}}</textarea></label>
<label>Line height <input type="text" name="line_height" value="{{.Values.Style.LineHeight}}" required></label>
<label>Paragraph indent <input type="text" name="paragraph_indent" value="{{.Values.Style.ParagraphIndent}}" required></label>
<label>Paragraph spacing <input type="text" name="paragraph_spacing" value="{{.Values.Style.ParagraphSpacing}}" required></label>
<label>Text align <input type="text" name="text_align" value="{{.Values.Style.TextAlign}}" required></label>
</fieldset>
<fieldset><legend>Kindle page</legend><label><input type="checkbox" name="kindle_show_epub" value="1" {{if .Values.KindleShowEPUB}}checked{{end}}> Show latest EPUB alongside latest AZW3</label></fieldset>
<fieldset><legend>Proofreading defaults</legend>
<label>Codex model (blank uses Codex default) <input type="text" name="proofread_model" value="{{.Values.Proofread.Model}}"></label>
<label>Batch size (characters) <input type="number" name="proofread_batch_size" min="1000" max="50000" value="{{.Values.Proofread.BatchSize}}" required></label>
<label>Concurrency <input type="number" name="proofread_concurrency" min="1" max="8" value="{{.Values.Proofread.Concurrency}}" required></label>
</fieldset>
<button type="submit">Save global defaults</button></form>
<fieldset><legend>Library diagnostics</legend><p>Database schema: v{{.System.SchemaVersion}} · journal: {{.System.JournalMode}}</p><p>Library writable: {{if .System.LibraryWritable}}yes{{else}}no{{end}} · free space: {{.SystemFree}}</p><p>Generation slot: {{.System.RunningGeneration}} running, {{.System.QueuedGeneration}} queued</p><p>Proofreading slot: {{.System.RunningProofread}} running, {{.System.QueuedProofread}} queued</p><p class="muted">Both slots select tasks from the same persistent FIFO sequence.</p></fieldset>
<fieldset><legend>Dependency diagnostics</legend><p>python3: {{if .Diagnostics.PythonPath}}{{.Diagnostics.PythonPath}} — {{.Diagnostics.PythonVersion}}{{else}}not found{{end}}</p><p>Codex CLI: {{if .Diagnostics.CodexPath}}{{.Diagnostics.CodexPath}} — {{.Diagnostics.CodexVersion}}{{else}}not found{{end}}</p><p>Required non-interactive flags: {{if .Diagnostics.CodexFlagsReady}}available{{else}}missing or unsupported{{end}}</p><form method="post" action="/settings/check-codex"><button type="submit">Check Codex login</button></form><p class="muted">The explicit check runs <code>codex login status</code> and does not send book content or invoke a model.</p></fieldset>
</body></html>`))

var txtPreviewTemplate = template.Must(template.New("txt-preview").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>TXT Preview — Kindle Go</title></head><body>
<p><a href="/books/{{.BookID}}">← Back to book</a></p><h1>TXT Preview</h1>
<dl><dt>Charset</dt><dd>{{.Charset}}</dd><dt>Lines</dt><dd>original {{.OriginalLines}}, dropped {{.DroppedLines}}, blank {{.BlankLines}}, merged {{.MergedLines}}</dd><dt>Structure</dt><dd>{{.Sections}} sections, {{.Paragraphs}} paragraphs</dd></dl>
<h2>Table of contents</h2><ul>{{range .TOC}}<li>{{.}}</li>{{else}}<li>正文</li>{{end}}</ul>
</body></html>`))

var kindleTemplate = template.Must(template.New("kindle").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Kindle Downloads</title>
</head>
<body>
  <h1>Downloads</h1>
  <p>{{if .ShowAll}}<a href="/">Recent</a>{{else}}<a href="/?all=1">All files</a>{{end}}</p>
  {{range .Records}}
    <h2>{{.OriginalName}}</h2>
    <p>{{.UploadedAt}}</p>
    <ul>
    {{range .Files}}
      <li><a href="{{.URL}}">{{.Name}}</a> ({{.Format}}, {{.Size}})</li>
    {{end}}
    </ul>
  {{else}}
    <p>No downloadable files.</p>
  {{end}}
</body>
</html>`))
