package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
)

const recentWindow = 24 * time.Hour

func (h Handler) handleWebIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, err := h.library.ListBooks(r.Context(), library.BookQuery{
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
		if pending, ok := h.library.PendingDuplicate(token); ok {
			data.Duplicate = duplicateView{Token: token, Filename: pending.Filename, Existing: h.recordViews(pending.Existing)}
		} else {
			data.Message = "Duplicate confirmation expired; import the file again."
		}
	}
	if err := webTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleKindleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	values, err := h.settings.Current(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	books, err := h.library.LatestKindleFiles(r.Context(), values.KindleShowEPUB)
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
	result, err := h.library.Import(r.Context(), library.ImportRequest{Filename: header.Filename, Reader: file, Now: time.Now()})
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
	book, err := h.library.ConfirmImport(r.Context(), token, r.Form.Get("action"))
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
	detail, ok, err := h.library.GetBookDetail(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	tasks, err := h.tasks.List(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	values, err := h.proofreads.Runs(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var runs []proofreadRunView
	for _, value := range values {
		runs = append(runs, proofreadRunView{ID: value.ID, Format: strings.ToUpper(value.Format), Model: value.Model, CompletedAt: value.CompletedAt.Format("2006-01-02 15:04")})
	}
	generationByFile := make(map[string]generation.InputAssessment)
	var generationInputs []generation.InputAssessment
	assessments, err := h.generation.Inputs(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Defaults = defaults
	data.DropRegex = strings.Join(defaults.TXT.DropRegex, "\n")
	replacements, _ := json.Marshal(defaults.TXT.Replace)
	data.ReplaceJSON = string(replacements)
	if err := bookTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleGenerate(w http.ResponseWriter, r *http.Request, bookID string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	formats := r.Form["format"]
	if len(formats) == 0 {
		formats = []string{"azw3"}
	}
	options, err := generationOptionsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, err = h.generation.Create(r.Context(), generation.CreateRequest{
		BookID: bookID, InputFileID: r.Form.Get("input_file_id"), Formats: formats,
		Options: options,
	})
	if err != nil {
		http.Redirect(w, r, "/books/"+bookID+"?message="+urlMessage("generate failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books/"+bookID+"?message=task+queued", http.StatusSeeOther)
}

func (h Handler) handleTXTPreview(w http.ResponseWriter, r *http.Request, bookID string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	options, err := generationOptionsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	analysis, err := h.generation.PreviewTXT(r.Context(), generation.PreviewRequest{
		BookID: bookID, InputFileID: r.Form.Get("input_file_id"), Options: options,
	})
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

func generationOptionsFromForm(r *http.Request) (generation.Options, error) {
	options := generation.Options{
		Title: r.Form.Get("title"), Author: r.Form.Get("author"), Language: r.Form.Get("language"),
		H1Regex: r.Form.Get("h1_regex"), H2Regex: r.Form.Get("h2_regex"),
		SplitLevel: parseInt(r.Form.Get("split_level")), LineHeight: parseFloat(r.Form.Get("line_height")),
		ParagraphSpacing: r.Form.Get("paragraph_spacing"), ParagraphIndent: r.Form.Get("paragraph_indent"), TextAlign: r.Form.Get("text_align"),
		Cover: formBool(r, "cover"), MergeLines: formBool(r, "merge_lines"), TrimBlankLines: formBool(r, "trim_blank_lines"),
	}
	if r.Form.Has("drop_regex") {
		dropRegex := nonBlankLines(r.Form.Get("drop_regex"))
		options.DropRegex = &dropRegex
	}
	if r.Form.Has("replace_json") {
		replacements := []txtconfig.ReplaceRule{}
		if raw := strings.TrimSpace(r.Form.Get("replace_json")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &replacements); err != nil {
				return generation.Options{}, fmt.Errorf("replacement rules must be valid JSON: %w", err)
			}
		}
		options.Replace = &replacements
	}
	return options, nil
}

func formBool(r *http.Request, name string) *bool {
	if r.Form.Get(name+"_present") == "" {
		return nil
	}
	value := r.Form.Get(name) != ""
	return &value
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
	if err := h.library.DeleteBook(r.Context(), bookID); err != nil {
		http.Redirect(w, r, "/books/"+bookID+"?message="+urlMessage(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books?message=book+deleted", http.StatusSeeOther)
}

func (h Handler) handleFileRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/files/")
	if strings.HasSuffix(rest, "/delete") && r.Method == http.MethodPost {
		fileID := strings.TrimSuffix(rest, "/delete")
		if fileID == "" || strings.Contains(fileID, "/") {
			http.NotFound(w, r)
			return
		}
		_, file, err := h.library.DownloadFile(r.Context(), fileID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := h.library.DeleteFile(r.Context(), fileID); err != nil {
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
	values, err := h.settings.Current(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.handleDownloadWith(w, r, func(ctx context.Context, id string) (string, library.File, error) {
		return h.library.DownloadKindleFile(ctx, id, values.KindleShowEPUB)
	})
}

func (h Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	h.handleDownloadWith(w, r, h.library.DownloadFile)
}

func (h Handler) handleDownloadWith(w http.ResponseWriter, r *http.Request, resolve func(context.Context, string) (string, library.File, error)) {
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
	path, file, err := resolve(r.Context(), fileID)
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
