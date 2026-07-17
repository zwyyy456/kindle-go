package server

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
)

const recentWindow = 24 * time.Hour

type Handler struct {
	Library    *library.Service
	Generation *generation.Service
}

func (h Handler) WebMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleWebRoot)
	mux.HandleFunc("/books", h.handleBooks)
	mux.HandleFunc("/books/", h.handleBookRoute)
	mux.HandleFunc("/download/", h.handleDownload)
	return mux
}

func (h Handler) KindleMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleKindleIndex)
	mux.HandleFunc("/download/", h.handleDownload)
	return mux
}

func (h Handler) handleWebIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records := h.recordsFor(r)
	data := webPageData{
		Records: h.recordViews(records, false),
		ShowAll: r.URL.Query().Get("all") == "1",
		Message: r.URL.Query().Get("message"),
	}
	if token := r.URL.Query().Get("duplicate"); token != "" {
		if pending, ok := h.Library.PendingDuplicate(token); ok {
			data.Duplicate = duplicateView{Token: token, Filename: pending.Filename, Existing: h.recordViews(pending.Existing, false)}
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
	http.NotFound(w, r)
}

func (h Handler) handleKindleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	records := h.recordsFor(r)
	data := kindlePageData{
		Records: h.recordViews(records, true),
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
	http.Redirect(w, r, "/books/"+book.ID, http.StatusSeeOther)
}

func (h Handler) handleBookDetail(w http.ResponseWriter, r *http.Request, id string) {
	book, ok, err := h.Library.GetBook(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/books?message="+urlMessage("opened "+book.DisplayName), http.StatusSeeOther)
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
		BookID: bookID, Formats: formats,
		Options: generation.Options{
			Title: r.Form.Get("title"), Author: r.Form.Get("author"), Language: r.Form.Get("language"),
			H1Regex: r.Form.Get("h1_regex"), H2Regex: r.Form.Get("h2_regex"),
			SplitLevel: parseInt(r.Form.Get("split_level")), LineHeight: parseFloat(r.Form.Get("line_height")),
			ParagraphSpacing: r.Form.Get("paragraph_spacing"), ParagraphIndent: r.Form.Get("paragraph_indent"), TextAlign: r.Form.Get("text_align"),
		},
	})
	if err != nil {
		http.Redirect(w, r, "/books?message="+urlMessage("generate failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books?message=task+queued", http.StatusSeeOther)
}

func parseInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func parseFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value
}

func (h Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	recordID, kind, ok := parseDownloadPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	path, file, err := h.Library.ResolveFile(r.Context(), recordID, kind)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.DisplayName))
	http.ServeFile(w, r, path)
}

func (h Handler) recordsFor(r *http.Request) []library.Book {
	var records []library.Book
	var err error
	if r.URL.Query().Get("all") == "1" {
		records, err = h.Library.AllBooks(r.Context())
	} else {
		records, err = h.Library.RecentBooks(r.Context(), time.Now().Add(-recentWindow))
	}
	if err != nil {
		return nil
	}
	return records
}

func (h Handler) recordViews(records []library.Book, kindle bool) []recordView {
	views := make([]recordView, 0, len(records))
	for _, record := range records {
		view := recordView{
			ID:           record.ID,
			OriginalName: record.DisplayName,
			UploadedAt:   record.ImportedAt.Format("2006-01-02 15:04"),
			LastError:    record.LegacyLastError,
		}
		view.Files = displayFileViews(record, kindle)
		view.InputFormat = strings.ToLower(record.Original.Format)
		view.Convertible = !kindle && (view.InputFormat == "txt" || view.InputFormat == "epub")
		view.TitleDefault = strings.TrimSuffix(record.Original.DisplayName, filepath.Ext(record.Original.DisplayName))
		if view.TitleDefault == "" {
			view.TitleDefault = strings.TrimSuffix(record.DisplayName, filepath.Ext(record.DisplayName))
		}
		if len(view.Files) > 0 || !kindle {
			views = append(views, view)
		}
	}
	return views
}

func parseDownloadPath(path string) (recordID, kind string, ok bool) {
	rest := strings.TrimPrefix(path, "/download/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		return "", "", false
	}
	switch parts[1] {
	case "original", "output":
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

func displayFileViews(record library.Book, kindle bool) []fileView {
	original := record.Original
	output := record.LatestArtifact
	if kindle {
		if output.ID != "" && kindleFormat(output.Format) {
			return []fileView{newFileView(record.ID, "output", output)}
		}
		if original.ID != "" && kindleFormat(original.Format) {
			return []fileView{newFileView(record.ID, "original", original)}
		}
		return nil
	}

	var files []fileView
	if original.ID != "" {
		files = append(files, newFileView(record.ID, "original", original))
	}
	if output.ID != "" {
		files = append(files, newFileView(record.ID, "output", output))
	}
	return files
}

func newFileView(recordID, kind string, file library.File) fileView {
	return fileView{
		Kind:   kind,
		Name:   file.DisplayName,
		Format: strings.ToUpper(file.Format),
		Size:   humanSize(file.Size),
		URL:    downloadURL(recordID, kind),
	}
}

func downloadURL(recordID, kind string) string {
	return "/download/" + recordID + "/" + kind
}

func kindleFormat(format string) bool {
	switch strings.ToLower(format) {
	case "azw3", "mobi", "pdf", "txt":
		return true
	default:
		return false
	}
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
	Records   []recordView
	ShowAll   bool
	Message   string
	Duplicate duplicateView
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

type recordView struct {
	ID           string
	OriginalName string
	UploadedAt   string
	LastError    string
	Files        []fileView
	Convertible  bool
	InputFormat  string
	TitleDefault string
}

type fileView struct {
	Kind   string
	Name   string
	Format string
	Size   string
	URL    string
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

  <p>
    {{if .ShowAll}}<a href="/books">Show recent uploads</a>{{else}}<a href="/books?all=1">Show all history</a>{{end}}
  </p>

  {{range .Records}}
  <section class="record">
    <h2>{{.OriginalName}}</h2>
    <p class="muted">Uploaded {{.UploadedAt}}</p>
    {{if .LastError}}<p class="error">{{.LastError}}</p>{{end}}
    <div class="files">
      {{range .Files}}
        <p><a href="{{.URL}}">{{.Name}}</a> <span class="muted">{{.Kind}}, {{.Format}}, {{.Size}}</span></p>
      {{end}}
    </div>
    {{if .Convertible}}
    <details>
      <summary>Convert</summary>
      <form method="post" action="/books/{{.ID}}/generate">
        <label>Format
          <select name="format">
            <option value="azw3">AZW3</option>
            {{if eq .InputFormat "txt"}}<option value="epub">EPUB</option>{{end}}
          </select>
        </label>
        <label>Title <input type="text" name="title" value="{{.TitleDefault}}"></label>
        <label>Author <input type="text" name="author"></label>
        <label>Language <input type="text" name="language"></label>
        {{if eq .InputFormat "txt"}}
        <label>H1 regex <input type="text" name="h1_regex"></label>
        <label>H2 regex <input type="text" name="h2_regex"></label>
        <label>Split level <input type="number" name="split_level" min="1" max="2"></label>
        <label>Line height <input type="text" name="line_height"></label>
        <label>Paragraph spacing <input type="text" name="paragraph_spacing"></label>
        <label>Paragraph indent <input type="text" name="paragraph_indent"></label>
        <label>Text align <input type="text" name="text_align"></label>
        {{end}}
        <button type="submit">Convert</button>
      </form>
    </details>
    {{end}}
  </section>
  {{else}}
  <p>No uploads yet.</p>
  {{end}}
</body>
</html>`))

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
