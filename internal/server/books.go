package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func (h Handler) handleWebIndex(w http.ResponseWriter, r *http.Request) {
	page, err := h.library.ListBooks(r.Context(), library.BookQuery{
		Search: r.URL.Query().Get("q"), Sort: r.URL.Query().Get("sort"), StatusFilter: r.URL.Query().Get("status"), Page: parseInt(r.URL.Query().Get("page")),
	})
	if err != nil {
		h.writeInternalError(w, r, err)
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
		if pending, ok := h.library.PendingDuplicate(token); ok {
			data.Duplicate = duplicateView{Token: token, Filename: pending.Filename, Existing: h.recordViews(pending.Existing)}
		} else {
			data.Message = "Duplicate confirmation expired; import the file again."
		}
	}
	if err := webTemplate.Execute(w, data); err != nil {
		h.writeInternalError(w, r, err)
	}
}

func (h Handler) handleImport(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.library.Import(r.Context(), library.ImportRequest{Filename: header.Filename, Reader: file, Now: time.Now()})
	if err != nil {
		code := library.ErrorCode(err)
		if code == "" {
			h.writeInternalError(w, r, err)
			return
		}
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

func (h Handler) handleImportConfirm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	book, err := h.library.ConfirmImport(r.Context(), r.PathValue("token"), r.Form.Get("action"))
	if err != nil {
		h.redirectActionError(w, r, "/books", err, "Import confirmation failed. Import the file again.")
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

func (h Handler) handleBookDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("bookID")
	detail, ok, err := h.library.GetBookDetail(r.Context(), id)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	tasks, err := h.tasks.List(r.Context(), id)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	values, err := h.proofreads.Runs(r.Context(), id)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	var runs []proofreadRunView
	runsByID := make(map[string]proofread.Run, len(values))
	for _, value := range values {
		runsByID[value.ID] = value
		runs = append(runs, proofreadRunView{ID: value.ID, Format: strings.ToUpper(value.Format), Model: value.Model, CompletedAt: value.CompletedAt.Format("2006-01-02 15:04")})
	}
	tasksByID := make(map[string]task.Task, len(tasks))
	for _, value := range tasks {
		tasksByID[value.ID] = value
	}
	sourcesByID := make(map[string]library.File, len(detail.Files))
	for _, file := range detail.Files {
		sourcesByID[file.ID] = file
	}
	generationByFile := make(map[string]generation.InputAssessment)
	var generationInputs []generation.InputAssessment
	assessments, err := h.generation.Inputs(r.Context(), id)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	for _, assessment := range assessments {
		generationByFile[assessment.ID] = assessment
		if assessment.Available {
			generationInputs = append(generationInputs, assessment)
		}
	}
	files := make([]fileView, 0, len(detail.Files))
	for _, file := range detail.Files {
		view := newFileView(file)
		enrichFileView(&view, file, sourcesByID, tasksByID, runsByID)
		if assessment, ok := generationByFile[file.ID]; ok {
			view.CompatibilityStatus = assessment.CompatibilityStatus
		}
		files = append(files, view)
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
	defaults, err := h.settings.Current(r.Context())
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	data.Defaults = defaults
	data.DropRegex = strings.Join(defaults.TXT.DropRegex, "\n")
	replacements, _ := json.Marshal(defaults.TXT.Replace)
	data.ReplaceJSON = string(replacements)
	if err := bookTemplate.Execute(w, data); err != nil {
		h.writeInternalError(w, r, err)
	}
}

func (h Handler) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	bookID := r.PathValue("bookID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Form.Get("confirm") != "delete" {
		http.Error(w, "deletion confirmation is required", http.StatusBadRequest)
		return
	}
	if err := h.library.DeleteBook(r.Context(), bookID); err != nil {
		h.redirectActionError(w, r, "/books/"+bookID, err, "Book deletion failed. Refresh the page and try again.")
		return
	}
	http.Redirect(w, r, "/books?message=book+deleted", http.StatusSeeOther)
}
